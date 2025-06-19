# Node Cordon Controller

The Node Cordon Controller monitors all nodes in the Kubernetes cluster and detects when nodes are cordoned (marked as unschedulable). This is useful for:

- **Maintenance tracking**: Detecting when nodes are taken offline for maintenance
- **Debugging**: Understanding when and why nodes become unavailable
- **Automation**: Triggering remediation actions when nodes are cordoned
- **Monitoring**: Alerting on node availability issues

## How it Works

The controller watches for changes to Node resources and specifically monitors the `spec.unschedulable` field. When a node is cordoned, the controller:

1. Logs detailed information about the cordoned node
2. Records node conditions, taints, and resource status
3. Provides hooks for custom remediation logic

## Key Features

- **Event Filtering**: Only processes events when the cordon status actually changes
- **Detailed Logging**: Captures comprehensive node state information
- **Graceful Handling**: Properly handles node deletions and missing nodes
- **Extensible**: Easy to add custom logic for handling cordoned nodes

## Example Usage

### Testing the Controller

You can test the controller by cordoning a node:

```bash
# Cordon a node
kubectl cordon <node-name>

# Check the controller logs to see the detection
kubectl logs -n <operator-namespace> <operator-pod> | grep "Node is cordoned"

# Uncordon the node
kubectl uncordon <node-name>
```

### Sample Log Output

When a node is cordoned, you'll see logs like:

```
INFO    Node is cordoned (unschedulable)    {"node": "worker-1", "creationTimestamp": "2024-01-01T10:00:00Z", "taints": 1}
INFO    Node resource status    {"node": "worker-1", "capacity-cpu": "4", "capacity-memory": "8Gi", "allocatable-cpu": "4", "allocatable-memory": "8Gi"}
INFO    Node has taint    {"node": "worker-1", "key": "node.kubernetes.io/unschedulable", "value": "", "effect": "NoSchedule"}
```

## RBAC Permissions

The controller requires the following permissions:

```yaml
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["nodes/status"]
  verbs: ["get"]
```

## Customization

To add custom logic when a node is cordoned, modify the `Reconcile` function in `internal/controller/node_cordon_controller.go`:

```go
if node.Spec.Unschedulable {
    logger.Info("Node is cordoned (unschedulable)", 
        "node", node.Name,
        "creationTimestamp", node.CreationTimestamp,
        "taints", len(node.Spec.Taints))
    
    // Add your custom logic here:
    // - Send alerts to monitoring systems
    // - Update metrics
    // - Trigger remediation workflows
    // - Log to external systems
    
    // Example: Send to metrics system
    // metrics.NodeCordonedTotal.WithLabelValues(node.Name).Inc()
    
    // Example: Trigger alert
    // alertManager.SendAlert("NodeCordoned", node.Name)
}
```

## Monitoring Integration

The controller can be easily integrated with monitoring systems:

### Prometheus Metrics
Add custom metrics to track cordon events:

```go
var (
    nodeCordonedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "node_cordoned_total",
            Help: "Total number of node cordon events",
        },
        []string{"node_name"},
    )
)
```

### Alerting
Set up alerts for when nodes are cordoned:

```yaml
groups:
- name: node.rules
  rules:
  - alert: NodeCordoned
    expr: increase(node_cordoned_total[5m]) > 0
    labels:
      severity: warning
    annotations:
      summary: "Node {{ $labels.node_name }} has been cordoned"
```

## Architecture

The controller uses the controller-runtime framework and implements:

- **Reconciler Pattern**: Processes node events through a reconcile loop
- **Event Filtering**: Uses predicates to only process relevant changes
- **Error Handling**: Gracefully handles missing or deleted nodes
- **Structured Logging**: Provides detailed, structured log output

This makes it efficient and reliable for production use while being easily extensible for custom requirements. 