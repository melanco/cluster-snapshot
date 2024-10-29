package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ClusterSnapshot represents the complete state of a cluster at a point in time
type ClusterSnapshot struct {
	Timestamp      time.Time       `json:"timestamp"`
	ClusterName    string          `json:"clusterName"`
	Nodes          []Resource      `json:"nodes"`
	NodeResources  []ResourceUsage `json:"nodeResources"`
	Operators      []Resource      `json:"operators"`
	Pods           []Resource      `json:"pods"`
	PodResources   []ResourceUsage `json:"podResources"`
	DaemonSets     []Resource      `json:"daemonSets"`
	StatefulSets   []Resource      `json:"statefulSets"`
	SingleReplicas []string        `json:"singleReplicas"`
	MCPStatus      []Resource      `json:"mcpStatus"`
}

// Resource represents a generic Kubernetes resource
type Resource struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Status    string            `json:"status"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// ResourceUsage represents resource consumption metrics
type ResourceUsage struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
}

// runCommand executes a command and returns its output
func runCommand(cmd string) (string, error) {
	// Execute the command using 'bash -c'
	output, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// takeSnapshot captures the current state of the cluster
func takeSnapshot() (*ClusterSnapshot, error) {
	snapshot := &ClusterSnapshot{
		Timestamp: time.Now(),
	}

	// Get cluster name
	if output, err := runCommand("oc get infrastructure cluster -o jsonpath='{.status.infrastructureName}'"); err == nil {
		parts := strings.Split(strings.Trim(output, "'"), "-")
		if len(parts) >= 2 {
			snapshot.ClusterName = strings.Join(parts[:2], "-")
		} else {
			snapshot.ClusterName = strings.Join(parts, "-")
		}
	}

	// Get nodes
	if output, err := runCommand("oc get nodes -o json"); err == nil {
		var nodes struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &nodes)
		for _, node := range nodes.Items {
			snapshot.Nodes = append(snapshot.Nodes, Resource{
				Name:   node.Metadata.Name,
				Status: getNodeStatus(node.Status.Conditions),
			})
		}
	}

	// Get NodeResources
	if output, err := runCommand("oc adm top nodes"); err == nil {
		snapshot.NodeResources = parseNodeResourceUsage(output)
	}

	// Get Operators
	if output, err := runCommand("oc get co -o json"); err == nil {
		var operators struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &operators)
		for _, op := range operators.Items {
			status := getOperatorStatus(op.Status.Conditions)
			snapshot.Operators = append(snapshot.Operators, Resource{
				Name:   op.Metadata.Name,
				Status: status,
			})
		}
	}

	// Get Pods
	if output, err := runCommand("oc get pods -A -o json"); err == nil {
		var pods struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &pods)
		for _, pod := range pods.Items {
			snapshot.Pods = append(snapshot.Pods, Resource{
				Name:      pod.Metadata.Name,
				Namespace: pod.Metadata.Namespace,
				Status:    pod.Status.Phase,
			})
		}
	}

	// Get PodResources
	if output, err := runCommand("oc adm top pods -A"); err == nil {
		snapshot.PodResources = parsePodResourceUsage(output)
	}

	// Get DaemonSets
	if output, err := runCommand("oc get daemonsets -A -o json"); err == nil {
		var daemonsets struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					DesiredNumberScheduled int `json:"desiredNumberScheduled"`
					NumberReady            int `json:"numberReady"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &daemonsets)
		for _, ds := range daemonsets.Items {
			snapshot.DaemonSets = append(snapshot.DaemonSets, Resource{
				Name:      ds.Metadata.Name,
				Namespace: ds.Metadata.Namespace,
				Status:    fmt.Sprintf("%d/%d", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled),
			})
		}
	}

	// Get StatefulSets
	if output, err := runCommand("oc get sts -A -o json"); err == nil {
		var statefulsets struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					ReadyReplicas int `json:"readyReplicas"`
					Replicas      int `json:"replicas"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &statefulsets)
		for _, sts := range statefulsets.Items {
			snapshot.StatefulSets = append(snapshot.StatefulSets, Resource{
				Name:      sts.Metadata.Name,
				Namespace: sts.Metadata.Namespace,
				Status:    fmt.Sprintf("%d/%d", sts.Status.ReadyReplicas, sts.Status.Replicas),
			})
		}
	}

	// Get Deployments with 1 replica
	if output, err := runCommand("oc get deployments -A -o json"); err == nil {
		var deployments struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Replicas int `json:"replicas"`
				} `json:"spec"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &deployments)
		for _, dep := range deployments.Items {
			if dep.Spec.Replicas == 1 {
				snapshot.SingleReplicas = append(snapshot.SingleReplicas, fmt.Sprintf("%s/%s", dep.Metadata.Namespace, dep.Metadata.Name))
			}
		}
	}

	// Get MachineConfigPools
	if output, err := runCommand("oc get mcp -o json"); err == nil {
		var mcps struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal([]byte(output), &mcps)
		for _, mcp := range mcps.Items {
			status := getMCPStatus(mcp.Status.Conditions)
			snapshot.MCPStatus = append(snapshot.MCPStatus, Resource{
				Name:   mcp.Metadata.Name,
				Status: status,
			})
		}
	}

	return snapshot, nil
}

// getNodeStatus determines the overall node status from conditions
func getNodeStatus(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) string {
	for _, condition := range conditions {
		if condition.Type == "Ready" {
			return condition.Status
		}
	}
	return "Unknown"
}

// getOperatorStatus determines the overall operator status from conditions
func getOperatorStatus(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) string {
	for _, condition := range conditions {
		if condition.Type == "Available" {
			return condition.Status
		}
	}
	return "Unknown"
}

// getMCPStatus determines the overall MCP status from conditions
func getMCPStatus(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) string {
	for _, condition := range conditions {
		if condition.Type == "Updated" {
			return condition.Status
		}
	}
	return "Unknown"
}

// parseNodeResourceUsage parses the output of 'oc adm top nodes'
func parseNodeResourceUsage(output string) []ResourceUsage {
	var usages []ResourceUsage
	lines := strings.Split(output, "\n")
	if len(lines) < 1 {
		return usages
	}
	// Skip header
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		usage := ResourceUsage{
			Name:   fields[0],
			CPU:    fields[1],
			Memory: fields[3],
		}
		usages = append(usages, usage)
	}
	return usages
}

// parsePodResourceUsage parses the output of 'oc adm top pods -A'
func parsePodResourceUsage(output string) []ResourceUsage {
	var usages []ResourceUsage
	lines := strings.Split(output, "\n")
	if len(lines) < 1 {
		return usages
	}
	// Skip header
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		usage := ResourceUsage{
			Namespace: fields[0],
			Name:      fields[1],
			CPU:       fields[2],
			Memory:    fields[3],
		}
		usages = append(usages, usage)
	}
	return usages
}

// Helper function to parse CPU strings (e.g., "68m" to float64)
func parseCPU(cpuStr string) (float64, error) {
	cpuStr = strings.TrimSpace(cpuStr)
	if strings.HasSuffix(cpuStr, "m") {
		valueStr := strings.TrimSuffix(cpuStr, "m")
		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			return 0, err
		}
		return value, nil // Value in millicores
	} else {
		// If no 'm', assume it's in cores
		value, err := strconv.ParseFloat(cpuStr, 64)
		if err != nil {
			return 0, err
		}
		return value * 1000, nil // Convert cores to millicores
	}
}

// Helper function to parse memory strings (e.g., "309Mi" to bytes)
func parseMemory(memStr string) (float64, error) {
	memStr = strings.TrimSpace(memStr)
	units := []struct {
		suffix string
		factor float64
	}{
		{"Ki", 1024},
		{"Mi", 1024 * 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"Ti", 1024 * 1024 * 1024 * 1024},
		{"Pi", 1024 * 1024 * 1024 * 1024 * 1024},
		{"Ei", 1024 * 1024 * 1024 * 1024 * 1024 * 1024},
		{"K", 1000},
		{"M", 1000 * 1000},
		{"G", 1000 * 1000 * 1000},
		{"T", 1000 * 1000 * 1000 * 1000},
		{"P", 1000 * 1000 * 1000 * 1000 * 1000},
		{"E", 1000 * 1000 * 1000 * 1000 * 1000 * 1000},
	}

	for _, unit := range units {
		if strings.HasSuffix(memStr, unit.suffix) {
			valueStr := strings.TrimSuffix(memStr, unit.suffix)
			value, err := strconv.ParseFloat(valueStr, 64)
			if err != nil {
				return 0, err
			}
			return value * unit.factor, nil
		}
	}

	// If no unit, assume bytes
	value, err := strconv.ParseFloat(memStr, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

// compareSnapshots compares two snapshots and returns the differences
func compareSnapshots(old, new *ClusterSnapshot) []string {
	var differences []string

	percentageThreshold := 500.0                    // Percentage increase threshold
	cpuAbsoluteThreshold := 100.0                   // CPU increase threshold in millicores
	memAbsoluteThreshold := 100.0 * 1024.0 * 1024.0 // Memory increase threshold in bytes (100Mi)

	// Compare Nodes
	nodeMap := make(map[string]Resource)
	for _, node := range old.Nodes {
		nodeMap[node.Name] = node
	}

	for _, newNode := range new.Nodes {
		if oldNode, exists := nodeMap[newNode.Name]; exists {
			if oldNode.Status != newNode.Status {
				differences = append(differences, fmt.Sprintf("Node %s status changed: %s -> %s",
					newNode.Name, oldNode.Status, newNode.Status))
			}
			delete(nodeMap, newNode.Name)
		} else {
			differences = append(differences, fmt.Sprintf("New node added: %s", newNode.Name))
		}
	}

	for name := range nodeMap {
		differences = append(differences, fmt.Sprintf("Node removed: %s", name))
	}

	// Compare PodResources with significant change
	podResourceMap := make(map[string]ResourceUsage)
	for _, usage := range old.PodResources {
		key := usage.Namespace + "/" + usage.Name
		podResourceMap[key] = usage
	}

	for _, newUsage := range new.PodResources {
		key := newUsage.Namespace + "/" + newUsage.Name
		if oldUsage, exists := podResourceMap[key]; exists {
			// Parse CPU and Memory values
			oldCPUValue, err1 := parseCPU(oldUsage.CPU)
			newCPUValue, err2 := parseCPU(newUsage.CPU)
			oldMemValue, err3 := parseMemory(oldUsage.Memory)
			newMemValue, err4 := parseMemory(newUsage.Memory)
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
				differences = append(differences, fmt.Sprintf("Pod %s resource usage parsing error", key))
				continue
			}

			// Calculate percentage increases and absolute differences
			cpuDiff := newCPUValue - oldCPUValue
			memDiff := newMemValue - oldMemValue

			var cpuIncrease, memIncrease float64
			if oldCPUValue > 0 {
				cpuIncrease = (cpuDiff / oldCPUValue) * 100
			} else {
				cpuIncrease = 0
			}
			if oldMemValue > 0 {
				memIncrease = (memDiff / oldMemValue) * 100
			} else {
				memIncrease = 0
			}

			// Check thresholds
			cpuSignificant := (cpuIncrease > percentageThreshold && cpuDiff > cpuAbsoluteThreshold) ||
				(oldCPUValue == 0 && newCPUValue > cpuAbsoluteThreshold)
			memSignificant := (memIncrease > percentageThreshold && memDiff > memAbsoluteThreshold) ||
				(oldMemValue == 0 && newMemValue > memAbsoluteThreshold)

			if cpuSignificant || memSignificant {
				// Format percentage increases
				cpuIncreaseStr := fmt.Sprintf("%.2f%%", cpuIncrease)
				memIncreaseStr := fmt.Sprintf("%.2f%%", memIncrease)

				// Handle cases where old value is zero
				if oldCPUValue == 0 {
					cpuIncreaseStr = "N/A"
				}
				if oldMemValue == 0 {
					memIncreaseStr = "N/A"
				}

				differences = append(differences, fmt.Sprintf("Pod %s resource usage increased significantly: CPU %s -> %s (%s), Memory %s -> %s (%s)",
					key, oldUsage.CPU, newUsage.CPU, cpuIncreaseStr, oldUsage.Memory, newUsage.Memory, memIncreaseStr))
			}

			delete(podResourceMap, key)
		} else {
			// New pod resource usage added
			newCPUValue, errCPU := parseCPU(newUsage.CPU)
			newMemValue, errMem := parseMemory(newUsage.Memory)
			if errCPU != nil || errMem != nil {
				continue
			}
			if newCPUValue > cpuAbsoluteThreshold || newMemValue > memAbsoluteThreshold {
				differences = append(differences, fmt.Sprintf("New pod resource usage added: %s (CPU: %s, Memory: %s)",
					key, newUsage.CPU, newUsage.Memory))
			}
		}
	}

	for key := range podResourceMap {
		differences = append(differences, fmt.Sprintf("Pod resource usage removed: %s", key))
	}

	// Compare Operators
	operatorMap := make(map[string]Resource)
	for _, op := range old.Operators {
		operatorMap[op.Name] = op
	}

	for _, newOp := range new.Operators {
		if oldOp, exists := operatorMap[newOp.Name]; exists {
			if oldOp.Status != newOp.Status {
				differences = append(differences, fmt.Sprintf("Operator %s status changed: %s -> %s",
					newOp.Name, oldOp.Status, newOp.Status))
			}
			delete(operatorMap, newOp.Name)
		} else {
			differences = append(differences, fmt.Sprintf("New operator added: %s", newOp.Name))
		}
	}

	for name := range operatorMap {
		differences = append(differences, fmt.Sprintf("Operator removed: %s", name))
	}

	// Compare Pods
	podMap := make(map[string]Resource)
	for _, pod := range old.Pods {
		key := pod.Namespace + "/" + pod.Name
		podMap[key] = pod
	}

	for _, newPod := range new.Pods {
		key := newPod.Namespace + "/" + newPod.Name
		if oldPod, exists := podMap[key]; exists {
			if oldPod.Status != newPod.Status {
				differences = append(differences, fmt.Sprintf("Pod %s status changed: %s -> %s",
					key, oldPod.Status, newPod.Status))
			}
			delete(podMap, key)
		} else {
			differences = append(differences, fmt.Sprintf("New pod added: %s", key))
		}
	}

	for key := range podMap {
		differences = append(differences, fmt.Sprintf("Pod removed: %s", key))
	}

	// Compare DaemonSets
	daemonSetMap := make(map[string]Resource)
	for _, ds := range old.DaemonSets {
		key := ds.Namespace + "/" + ds.Name
		daemonSetMap[key] = ds
	}

	for _, newDS := range new.DaemonSets {
		key := newDS.Namespace + "/" + newDS.Name
		if oldDS, exists := daemonSetMap[key]; exists {
			if oldDS.Status != newDS.Status {
				differences = append(differences, fmt.Sprintf("DaemonSet %s status changed: %s -> %s",
					key, oldDS.Status, newDS.Status))
			}
			delete(daemonSetMap, key)
		} else {
			differences = append(differences, fmt.Sprintf("New DaemonSet added: %s", key))
		}
	}

	for key := range daemonSetMap {
		differences = append(differences, fmt.Sprintf("DaemonSet removed: %s", key))
	}

	// Compare StatefulSets
	statefulSetMap := make(map[string]Resource)
	for _, sts := range old.StatefulSets {
		key := sts.Namespace + "/" + sts.Name
		statefulSetMap[key] = sts
	}

	for _, newSts := range new.StatefulSets {
		key := newSts.Namespace + "/" + newSts.Name
		if oldSts, exists := statefulSetMap[key]; exists {
			if oldSts.Status != newSts.Status {
				differences = append(differences, fmt.Sprintf("StatefulSet %s status changed: %s -> %s",
					key, oldSts.Status, newSts.Status))
			}
			delete(statefulSetMap, key)
		} else {
			differences = append(differences, fmt.Sprintf("New StatefulSet added: %s", key))
		}
	}

	for key := range statefulSetMap {
		differences = append(differences, fmt.Sprintf("StatefulSet removed: %s", key))
	}

	// Compare SingleReplicas
	singleReplicasMap := make(map[string]struct{})
	for _, dep := range old.SingleReplicas {
		singleReplicasMap[dep] = struct{}{}
	}

	for _, newDep := range new.SingleReplicas {
		if _, exists := singleReplicasMap[newDep]; exists {
			delete(singleReplicasMap, newDep)
		} else {
			differences = append(differences, fmt.Sprintf("New single-replica deployment added: %s", newDep))
		}
	}

	for dep := range singleReplicasMap {
		differences = append(differences, fmt.Sprintf("Single-replica deployment removed: %s", dep))
	}

	// Compare MCPStatus
	mcpMap := make(map[string]Resource)
	for _, mcp := range old.MCPStatus {
		mcpMap[mcp.Name] = mcp
	}

	for _, newMcp := range new.MCPStatus {
		if oldMcp, exists := mcpMap[newMcp.Name]; exists {
			if oldMcp.Status != newMcp.Status {
				differences = append(differences, fmt.Sprintf("MCP %s status changed: %s -> %s",
					newMcp.Name, oldMcp.Status, newMcp.Status))
			}
			delete(mcpMap, newMcp.Name)
		} else {
			differences = append(differences, fmt.Sprintf("New MCP added: %s", newMcp.Name))
		}
	}

	for name := range mcpMap {
		differences = append(differences, fmt.Sprintf("MCP removed: %s", name))
	}

	return differences
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cluster-snapshot [take|compare <snapshot1> <snapshot2>]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "take":
		snapshot, err := takeSnapshot()
		if err != nil {
			fmt.Printf("Error taking snapshot: %v\n", err)
			os.Exit(1)
		}

		filename := fmt.Sprintf("snapshot_%s_%s.json",
			snapshot.ClusterName,
			snapshot.Timestamp.Format("2006-01-02_15-04-05"))

		data, _ := json.MarshalIndent(snapshot, "", "  ")
		if err := os.WriteFile(filename, data, 0644); err != nil {
			fmt.Printf("Error saving snapshot: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Snapshot saved to %s\n", filename)

	case "compare":
		if len(os.Args) != 4 {
			fmt.Println("Usage: cluster-snapshot compare <snapshot1> <snapshot2>")
			os.Exit(1)
		}

		var snap1, snap2 ClusterSnapshot
		data1, err1 := os.ReadFile(os.Args[2])
		if err1 != nil {
			fmt.Printf("Error reading snapshot %s: %v\n", os.Args[2], err1)
			os.Exit(1)
		}
		data2, err2 := os.ReadFile(os.Args[3])
		if err2 != nil {
			fmt.Printf("Error reading snapshot %s: %v\n", os.Args[3], err2)
			os.Exit(1)
		}

		if err := json.Unmarshal(data1, &snap1); err != nil {
			fmt.Printf("Error parsing snapshot %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		if err := json.Unmarshal(data2, &snap2); err != nil {
			fmt.Printf("Error parsing snapshot %s: %v\n", os.Args[3], err)
			os.Exit(1)
		}

		differences := compareSnapshots(&snap1, &snap2)
		fmt.Printf("Comparing snapshots from %s to %s\n\n",
			snap1.Timestamp.Format("2006-01-02 15:04:05"),
			snap2.Timestamp.Format("2006-01-02 15:04:05"))

		if len(differences) == 0 {
			fmt.Println("No differences found")
		} else {
			fmt.Println("Differences found:")
			for _, diff := range differences {
				fmt.Printf("- %s\n", diff)
			}
		}
	default:
		fmt.Println("Unknown command. Usage: cluster-snapshot [take|compare <snapshot1> <snapshot2>]")
	}
}
