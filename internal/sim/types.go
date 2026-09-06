// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package sim

type Request struct {
	ClusterYAML string `json:"clusterYaml"`
	DesiredYAML string `json:"desiredYaml"`
}

type ResourceRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type Risk struct {
	Severity string      `json:"severity"`
	Code     string      `json:"code"`
	Title    string      `json:"title"`
	Detail   string      `json:"detail"`
	Resource ResourceRef `json:"resource"`
}

type NodeResult struct {
	Name           string  `json:"name"`
	CPUCapacity    float64 `json:"cpuCapacity"`
	CPUFree        float64 `json:"cpuFree"`
	MemoryCapacity int64   `json:"memoryCapacityBytes"`
	MemoryFree     int64   `json:"memoryFreeBytes"`
	GPUCapacity    int64   `json:"gpuCapacity"`
	GPUFree        int64   `json:"gpuFree"`
}

type Result struct {
	ClusterName          string       `json:"clusterName"`
	ObjectsAnalyzed      int          `json:"objectsAnalyzed"`
	ChangedObjects       int          `json:"changedObjects"`
	CreatedObjects       int          `json:"createdObjects"`
	DeletedObjects       int          `json:"deletedObjects"`
	PodsRestarted        int          `json:"podsRestarted"`
	PodsCreated          int          `json:"podsCreated"`
	UnschedulablePods    int          `json:"unschedulablePods"`
	ServicesAffected     int          `json:"servicesAffected"`
	NetworkPolicyChanges int          `json:"networkPolicyChanges"`
	PVCDestructiveRisks  int          `json:"pvcDestructiveRisks"`
	PDBRisks             int          `json:"pdbRisks"`
	QuotaRisks           int          `json:"quotaRisks"`
	CPUDeltaCores        float64      `json:"cpuDeltaCores"`
	MemoryDeltaBytes     int64        `json:"memoryDeltaBytes"`
	GPUDelta             int64        `json:"gpuDelta"`
	BlastRadius          int          `json:"blastRadius"`
	RiskLevel            string       `json:"riskLevel"`
	Verdict              string       `json:"verdict"`
	Risks                []Risk       `json:"risks"`
	Recommendations      []string     `json:"recommendations"`
	Nodes                []NodeResult `json:"nodes"`
}
