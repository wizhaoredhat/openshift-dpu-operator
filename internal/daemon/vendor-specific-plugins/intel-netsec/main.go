package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/jaypipes/ghw"
	pb "github.com/openshift/dpu-operator/dpu-api/gen"
	"github.com/openshift/dpu-operator/internal/utils"
	opi "github.com/opiproject/opi-api/network/evpn-gw/v1alpha1/gen/go"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	Version                              string = "0.0.1"
	IPv6AddrDpu                          string = "fe80::1"
	IPv6AddrHost                         string = "fe80::2"
	DefaultPort                          int32  = 8085
	IntelVendorID                        string = "8086"
	IntelNetSecHostVfDeviceID            string = "1889" // Intel Corporation Ethernet Adaptive Virtual Function
	IntelNetSecDpuSFPf0PCIeAddress       string = "0000:f4:00.0"
	IntelNetSecDpuSFPf1PCIeAddress       string = "0000:f4:00.1"
	IntelNetSecDpuBackplanef2PCIeAddress string = "0000:f4:00.2"
	IntelNetSecDpuBackplanef3PCIeAddress string = "0000:f4:00.3"
)

type intelNetSecVspServer struct {
	pb.UnimplementedLifeCycleServiceServer
	pb.UnimplementedNetworkFunctionServiceServer
	pb.UnimplementedDeviceServiceServer
	opi.UnimplementedBridgePortServiceServer
	log            logr.Logger
	grpcServer     *grpc.Server
	wg             sync.WaitGroup
	done           chan error
	startedWg      sync.WaitGroup
	pathManager    utils.PathManager
	version        string
	isDPUMode      bool
	dpuPcieAddress string
}

func SetSriovNumVfs(pciAddr string, numVfs int) error {
	klog.Infof("SetSriovNumVfs(): set NumVfs device %s numvfs %d", pciAddr, numVfs)
	numVfsFilePath := filepath.Join("/sys/bus/pci/devices", pciAddr, "sriov_numvfs")
	bs := []byte(strconv.Itoa(numVfs))
	err := os.WriteFile(numVfsFilePath, []byte("0"), os.ModeAppend)
	if err != nil {
		klog.Errorf("SetSriovNumVfs(): fail to reset NumVfs file path %s, err %v", numVfsFilePath, err)
		return err
	}
	if numVfs == 0 {
		return nil
	}
	err = os.WriteFile(numVfsFilePath, bs, os.ModeAppend)
	if err != nil {
		klog.Errorf("SetSriovNumVfs(): fail to set NumVfs file path %s, err %v", numVfsFilePath, err)
		return err
	}
	return nil
}

func (vsp *intelNetSecVspServer) GetVFs(pfPCIeAddress string) ([]string, error) {
	var pciVFAddresses []string

	pciInfo, err := ghw.PCI()
	if err != nil {
		return nil, err
	}

	bus := ghw.PCIAddressFromString(pfPCIeAddress).Bus
	for _, pci := range pciInfo.Devices {
		if ghw.PCIAddressFromString(pci.Address).Bus == bus {
			if pci.Vendor.ID == IntelVendorID &&
				pci.Product.ID == IntelNetSecHostVfDeviceID {
				pciVFAddresses = append(pciVFAddresses, pci.Address)
			}
		}
	}

	numVfs := len(pciVFAddresses)
	vsp.log.Info("GetVFs(): found VFs", "NumVFs", numVfs, "DpuPcieAddress", vsp.dpuPcieAddress)
	return pciVFAddresses, nil
}

func (vsp *intelNetSecVspServer) GetNetDevNameFromPCIeAddr(pcieAddress string) string {
	netInfo, err := ghw.Network()
	if err != nil {
		vsp.log.Error(err, "GetNetDevNameFromPCIeAddr(): failed to get network info")
		return ""
	}

	for _, nic := range netInfo.NICs {
		if nic.PCIAddress != nil && *nic.PCIAddress == pcieAddress {
			vsp.log.Info("GetNetDevNameFromPCIeAddr(): found DPU network device", "Name", nic.Name, "PCIAddress", *nic.PCIAddress)
			return nic.Name
		}
	}

	vsp.log.Error(nil, "GetNetDevNameFromPCIeAddr(): Network device not found", "PCIAddress", pcieAddress)
	return ""
}

func linkHasAddrgenmodeEui64(interfaceName string) bool {
	out, err := exec.Command("ip", "-d", "link", "show", "dev", interfaceName).Output()
	return err == nil && strings.Contains(string(out), "addrgenmode eui64")
}

func enableIPV6LinkLocal(interfaceName string, ipv6Addr string) error {
	// Tell NetworkManager to not manage our interface.
	err1 := exec.Command("nsenter", "-t", "1", "-m", "-u", "-n", "-i", "--", "nmcli", "device", "set", interfaceName, "managed", "no").Run()
	if err1 != nil {
		// This error may be fine. Maybe our host doesn't even run
		// NetworkManager. Ignore.
		klog.Infof("nmcli device set %s managed no failed with error %v", interfaceName, err1)
	}

	optimistic_dad_file := "/proc/sys/net/ipv6/conf/" + interfaceName + "/optimistic_dad"
	err1 = os.WriteFile(optimistic_dad_file, []byte("1"), os.ModeAppend)
	if err1 != nil {
		klog.Errorf("Error setting %s: %v", optimistic_dad_file, err1)
	}

	if linkHasAddrgenmodeEui64(interfaceName) {
		// Kernel may require that the SDP interfaces are up at all times (RHEL-90248).
		// If the addrgenmode is already eui64, assume we are fine and don't need to reset
		// it (and don't need to toggle the link state).
	} else {
		// Ensure to set addrgenmode and toggle link state (which can result in creating
		// the IPv6 link local address).
		err2 := exec.Command("ip", "link", "set", interfaceName, "addrgenmode", "eui64").Run()
		if err2 != nil {
			return fmt.Errorf("Error setting link %s addrgenmode: %v", interfaceName, err2)
		}
		err2 = exec.Command("ip", "link", "set", interfaceName, "down").Run()
		if err2 != nil {
			return fmt.Errorf("Error setting link %s down after setting addrgenmode: %v", interfaceName, err2)
		}
	}

	err := exec.Command("ip", "link", "set", interfaceName, "up").Run()
	if err != nil {
		return fmt.Errorf("Error setting link %s up: %v", interfaceName, err)
	}

	err = exec.Command("ip", "addr", "replace", ipv6Addr+"/64", "dev", interfaceName, "optimistic").Run()
	if err != nil {
		return fmt.Errorf("Error configuring IPv6 address %s/64 on link %s: %v", ipv6Addr, interfaceName, err)
	}
	return nil
}

func (vsp *intelNetSecVspServer) configureIP(dpuMode bool) (pb.IpPort, error) {
	var ifName string
	var addr string
	if dpuMode {
		// All NetSec DPU devices have the same internal PCIe Addresses. Netdev names can change with each RHEL release.
		ifName = vsp.GetNetDevNameFromPCIeAddr(IntelNetSecDpuBackplanef2PCIeAddress)
		addr = IPv6AddrDpu
	} else {
		ifName = vsp.GetNetDevNameFromPCIeAddr(vsp.dpuPcieAddress)
		addr = IPv6AddrHost
	}

	vsp.log.Info("configureIP(): DpuMode", "DpuMode", dpuMode, "IfName", ifName, "Addr", addr)

	err := enableIPV6LinkLocal(ifName, addr)
	addr = IPv6AddrDpu
	if err != nil {
		klog.Errorf("Error occurred in enabling IPv6 Link local Address: %v", err)
		return pb.IpPort{}, err
	}
	var connStr string
	if dpuMode {
		connStr = "[" + addr + "%" + ifName + "]"
	} else {
		connStr = "[" + addr + "%25" + ifName + "]"
	}

	klog.Infof("IPv6 Link Local Address Enabled IfName: %v, Connection String: %s", ifName, connStr)

	return pb.IpPort{
		Ip:   connStr,
		Port: DefaultPort,
	}, nil
}

func (vsp *intelNetSecVspServer) Init(ctx context.Context, in *pb.InitRequest) (*pb.IpPort, error) {
	klog.Infof("Received Init() request: DpuMode: %v DpuPcieAddress: %v", in.DpuMode, in.DpuPcieAddress)
	vsp.isDPUMode = in.DpuMode
	vsp.dpuPcieAddress = in.DpuPcieAddress
	ipPort, err := vsp.configureIP(in.DpuMode)

	return &pb.IpPort{
		Ip:   ipPort.Ip,
		Port: ipPort.Port,
	}, err
}

// TODO: Implement this
func (vsp *intelNetSecVspServer) GetDevices(ctx context.Context, in *pb.Empty) (*pb.DeviceListResponse, error) {
	klog.Info("Received GetDevices() request")
	devices := make(map[string]*pb.Device)

	vfs, err := vsp.GetVFs(vsp.dpuPcieAddress)
	if err != nil {
		klog.Errorf("Error getting VFs: %v", err)
		return nil, err
	}

	for _, vf := range vfs {
		klog.Infof("Adding device %s to the response", vf)
		devices[vf] = &pb.Device{
			ID:     vf,
			Health: "Healthy",
		}
	}

	return &pb.DeviceListResponse{
		Devices: devices,
	}, nil
}

// TODO: Implement this
func (vsp *intelNetSecVspServer) CreateBridgePort(ctx context.Context, in *opi.CreateBridgePortRequest) (*opi.BridgePort, error) {
	vsp.log.Info("Received CreateBridgePort() request", "BridgePortId", in.BridgePortId, "BridgePortId", in.BridgePortId)
	return &opi.BridgePort{}, nil
}

// TODO: Implement this
func (vsp *intelNetSecVspServer) DeleteBridgePort(ctx context.Context, in *opi.DeleteBridgePortRequest) (*emptypb.Empty, error) {
	vsp.log.Info("Received DeleteBridgePort() request", "Name", in.Name, "AllowMissing", in.AllowMissing)
	return nil, nil
}

// TODO: Implement this
func (vsp *intelNetSecVspServer) CreateNetworkFunction(ctx context.Context, in *pb.NFRequest) (*pb.Empty, error) {
	vsp.log.Info("Received CreateNetworkFunction() request", "Input", in.Input, "Output", in.Output)
	return nil, nil
}

// TODO: Implement this
func (vsp *intelNetSecVspServer) DeleteNetworkFunction(ctx context.Context, in *pb.NFRequest) (*pb.Empty, error) {
	vsp.log.Info("Received DeleteNetworkFunction() request", "Input", in.Input, "Output", in.Output)
	return nil, nil
}

// SetNumVfs function to set the number of VFs with the given context and VfCount
func (vsp *intelNetSecVspServer) SetNumVfs(ctx context.Context, in *pb.VfCount) (*pb.VfCount, error) {
	klog.Infof("Received SetNumVfs() request: VfCnt: %v", in.VfCnt)
	var err error

	if vsp.isDPUMode {
		err = SetSriovNumVfs(IntelNetSecDpuBackplanef2PCIeAddress, int(in.VfCnt))
	} else {
		err = SetSriovNumVfs(vsp.dpuPcieAddress, int(in.VfCnt))
	}

	return in, err
}

func (vsp *intelNetSecVspServer) Listen() (net.Listener, error) {
	err := vsp.pathManager.EnsureSocketDirExists(vsp.pathManager.VendorPluginSocket())
	if err != nil {
		return nil, fmt.Errorf("failed to create run directory for vendor plugin socket: %v", err)
	}
	listener, err := net.Listen("unix", vsp.pathManager.VendorPluginSocket())
	if err != nil {
		return nil, fmt.Errorf("failed to listen on the vendor plugin socket: %v", err)
	}
	vsp.grpcServer = grpc.NewServer()
	pb.RegisterNetworkFunctionServiceServer(vsp.grpcServer, vsp)
	pb.RegisterLifeCycleServiceServer(vsp.grpcServer, vsp)
	pb.RegisterDeviceServiceServer(vsp.grpcServer, vsp)
	opi.RegisterBridgePortServiceServer(vsp.grpcServer, vsp)
	klog.Infof("gRPC server is listening on %v", listener.Addr())

	return listener, nil
}

func (vsp *intelNetSecVspServer) Serve(listener net.Listener) error {
	vsp.wg.Add(1)
	go func() {
		vsp.version = Version
		klog.Infof("Starting Intel NetSec VSP Server: Version: %s", vsp.version)
		if err := vsp.grpcServer.Serve(listener); err != nil {
			vsp.done <- err
		} else {
			vsp.done <- nil
		}
		klog.Info("Stopping Intel NetSec VSP Server")
		vsp.wg.Done()
	}()

	// Block on any go routines writing to the done channel when an error occurs or they
	// are forced to exit.
	err := <-vsp.done

	vsp.grpcServer.Stop()
	vsp.wg.Wait()
	vsp.startedWg.Done()
	return err
}

func (vsp *intelNetSecVspServer) Stop() {
	vsp.grpcServer.Stop()
	vsp.done <- nil
	vsp.startedWg.Wait()
}

func WithPathManager(pathManager utils.PathManager) func(*intelNetSecVspServer) {
	return func(vsp *intelNetSecVspServer) {
		vsp.pathManager = pathManager
	}
}

func NewIntelNetSecVspServer(opts ...func(*intelNetSecVspServer)) *intelNetSecVspServer {
	var mode string
	flag.StringVar(&mode, "mode", "", "Mode for the daemon, can be either host or dpu")
	options := zap.Options{
		Development: true,
		Level:       zapcore.DebugLevel,
	}
	options.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&options)))
	vsp := &intelNetSecVspServer{
		log:         ctrl.Log.WithName("IntelNetSecVsp"),
		pathManager: *utils.NewPathManager("/"),
		done:        make(chan error),
	}

	for _, opt := range opts {
		opt(vsp)
	}

	return vsp
}

func main() {
	intelNetSecVspServer := NewIntelNetSecVspServer()
	listener, err := intelNetSecVspServer.Listen()

	if err != nil {
		intelNetSecVspServer.log.Error(err, "Failed to Listen Intel NetSec VSP server")
		return
	}
	err = intelNetSecVspServer.Serve(listener)
	if err != nil {
		intelNetSecVspServer.log.Error(err, "Failed to serve  Intel NetSec VSP server")
		return
	}
}
