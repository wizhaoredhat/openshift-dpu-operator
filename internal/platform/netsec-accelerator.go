package platform

import (
	"github.com/jaypipes/ghw"
	"github.com/openshift/dpu-operator/internal/daemon/plugin"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kind/pkg/errors"
)

const (
	IntelVendorID                string = "8086"
	IntelNetSecBackplaneDeviceID string = "124c"
	IntelNetSecSFPDeviceID       string = "124d"
	IntelNetSecHostDeviceID      string = "1599"
)

type NetsecAcceleratorDetector struct {
	Name string
}

func NewNetsecAcceleratorDetector() *NetsecAcceleratorDetector {
	return &NetsecAcceleratorDetector{Name: "Intel Netsec Accelerator"}
}

// IsDPU checks if the PCI device Attached to the host is a Marvell DPU
// It returns true if device has Marvell DPU
func (pi *NetsecAcceleratorDetector) IsDPU(pci ghw.PCIDevice) (bool, error) {
	if pci.Vendor.ID == IntelVendorID &&
		pci.Product.ID == IntelNetSecHostDeviceID {
		return true, nil
	}

	return false, nil
}

// IsDpuPlatform checks if the platform is a Marvell DPU
func (pi *NetsecAcceleratorDetector) IsDpuPlatform(platform Platform) (bool, error) {
	devices, err := platform.PciDevices()
	if err != nil {
		return false, errors.Errorf("Error getting devices: %v", err)
	}

	for _, pci := range devices {
		if pci.Vendor.ID == IntelVendorID &&
			pci.Product.ID == IntelNetSecBackplaneDeviceID {
			return true, nil
		}
	}

	return false, nil
}

func (pi *NetsecAcceleratorDetector) VspPlugin(dpuMode bool, vspImages map[string]string, client client.Client) (*plugin.GrpcPlugin, error) {
	template_vars := plugin.NewVspTemplateVars()
	template_vars.VendorSpecificPluginImage = vspImages[plugin.VspImageIntelNetSec]
	template_vars.Command = `[ "/vsp-intel-netsec" ]`
	return plugin.NewGrpcPlugin(dpuMode, client, plugin.WithVsp(template_vars))
}

// GetVendorName returns the name of the vendor
func (d *NetsecAcceleratorDetector) GetVendorName() string {
	return "intel"
}
