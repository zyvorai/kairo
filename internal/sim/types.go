// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

type Request struct {
	ClusterYAML string `json:"clusterYaml"`
	DesiredYAML string `json:"desiredYaml"`
	// TargetVersion is the Kubernetes minor version ("1.29" or "v1.29.3") the
	// manifests must be valid on. Empty means the newest version Kairo knows.
	TargetVersion string `json:"targetVersion,omitempty"`
	// Prices are optional user-supplied unit prices for a monthly cost estimate.
	Prices *Prices `json:"prices,omitempty"`
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
	ClusterName          string        `json:"clusterName"`
	ObjectsAnalyzed      int           `json:"objectsAnalyzed"`
	ChangedObjects       int           `json:"changedObjects"`
	CreatedObjects       int           `json:"createdObjects"`
	DeletedObjects       int           `json:"deletedObjects"`
	PodsRestarted        int           `json:"podsRestarted"`
	PodsCreated          int           `json:"podsCreated"`
	UnschedulablePods    int           `json:"unschedulablePods"`
	ServicesAffected     int           `json:"servicesAffected"`
	NetworkPolicyChanges int           `json:"networkPolicyChanges"`
	PVCDestructiveRisks  int           `json:"pvcDestructiveRisks"`
	PDBRisks             int           `json:"pdbRisks"`
	QuotaRisks           int           `json:"quotaRisks"`
	LimitRangeRisks      int           `json:"limitRangeRisks"`
	SecurityRisks        int           `json:"securityRisks"`
	ExposureChanges      int           `json:"exposureChanges"`
	RBACRisks            int           `json:"rbacRisks"`
	HPARisks             int           `json:"hpaRisks"`
	RolloutRisks         int           `json:"rolloutRisks"`
	RolloutStalls        int           `json:"rolloutStalls"`
	APIRemoved           int           `json:"apiRemoved"`
	APIDeprecated        int           `json:"apiDeprecated"`
	TargetVersion        string        `json:"targetVersion"`
	CPUDeltaCores        float64       `json:"cpuDeltaCores"`
	MemoryDeltaBytes     int64         `json:"memoryDeltaBytes"`
	GPUDelta             int64         `json:"gpuDelta"`
	BlastRadius          int           `json:"blastRadius"`
	RiskLevel            string        `json:"riskLevel"`
	Verdict              string        `json:"verdict"`
	Risks                []Risk        `json:"risks"`
	Recommendations      []string      `json:"recommendations"`
	Nodes                []NodeResult  `json:"nodes"`
	Changes              []Change      `json:"changes,omitempty"`
	Cost                 *CostEstimate `json:"cost,omitempty"`
}
