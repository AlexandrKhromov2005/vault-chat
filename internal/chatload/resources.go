package chatload

import "sort"

// GeneratorContainer is the Compose service of the generator; its usage is
// reported but is not part of the stand.
const GeneratorContainer = "loadgen"

// Snapshot is what the orchestrator (tests/load/chat_load.py) reads from the
// Docker engine, PostgreSQL and Redis at the edge of a phase. Counters are
// cumulative; MemoryPeak is the highest usage since the previous snapshot.
type Snapshot struct {
	Time       float64                      `json:"time"`
	Containers map[string]ContainerSnapshot `json:"containers"`
	Postgres   map[string]PostgresSnapshot  `json:"postgres"`
	Redis      map[string]RedisSnapshot     `json:"redis"`
}

// ContainerSnapshot is the cgroup usage of one Compose service.
type ContainerSnapshot struct {
	CPUNanoseconds int64 `json:"cpu_ns"`
	Memory         int64 `json:"memory"`
	MemoryPeak     int64 `json:"memory_peak"`
	MemoryLimit    int64 `json:"memory_limit"`
	Restarts       int   `json:"restarts"`
	OOMKilled      bool  `json:"oom_killed"`
}

// PostgresSnapshot is pg_stat_database of the service's database.
type PostgresSnapshot struct {
	Commits    int64 `json:"xact_commit"`
	Rollbacks  int64 `json:"xact_rollback"`
	Inserted   int64 `json:"tup_inserted"`
	Fetched    int64 `json:"tup_fetched"`
	BlocksRead int64 `json:"blks_read"`
	BlocksHit  int64 `json:"blks_hit"`
	Deadlocks  int64 `json:"deadlocks"`
	Backends   int64 `json:"numbackends"`
	TempBytes  int64 `json:"temp_bytes"`
}

// RedisSnapshot is part of Redis INFO.
type RedisSnapshot struct {
	Commands       int64 `json:"total_commands_processed"`
	InputBytes     int64 `json:"total_net_input_bytes"`
	OutputBytes    int64 `json:"total_net_output_bytes"`
	UsedMemory     int64 `json:"used_memory"`
	Clients        int64 `json:"connected_clients"`
	PubSubChannels int64 `json:"pubsub_channels"`
	Rejected       int64 `json:"rejected_connections"`
}

// Resources is the usage of the stand during one phase.
type Resources struct {
	Containers map[string]ContainerUsage `json:"containers"`
	// StandCPUCores is the CPU time of all stand containers per second.
	StandCPUCores float64 `json:"stand_cpu_cores"`
	// Busiest is the stand container that used the most CPU.
	Busiest  string                   `json:"busiest"`
	Postgres map[string]PostgresUsage `json:"postgres"`
	Redis    map[string]RedisUsage    `json:"redis"`
}

// ContainerUsage is the CPU and memory of one container during a phase.
type ContainerUsage struct {
	CPUCores         float64 `json:"cpu_cores"`
	MemoryPeakBytes  int64   `json:"memory_peak_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	MemoryRatio      float64 `json:"memory_ratio"`
	Restarts         int     `json:"restarts"`
	OOMKilled        bool    `json:"oom_killed"`
}

// PostgresUsage is the work of one database during a phase.
type PostgresUsage struct {
	TransactionsPerSecond float64 `json:"transactions_per_second"`
	InsertsPerSecond      float64 `json:"inserts_per_second"`
	FetchesPerSecond      float64 `json:"fetches_per_second"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	Deadlocks             int64   `json:"deadlocks"`
	TempBytes             int64   `json:"temp_bytes"`
	Backends              int64   `json:"backends"`
}

// RedisUsage is the work of one Redis during a phase.
type RedisUsage struct {
	CommandsPerSecond    float64 `json:"commands_per_second"`
	InputBytesPerSecond  float64 `json:"input_bytes_per_second"`
	OutputBytesPerSecond float64 `json:"output_bytes_per_second"`
	UsedMemoryBytes      int64   `json:"used_memory_bytes"`
	Clients              int64   `json:"clients"`
	PubSubChannels       int64   `json:"pubsub_channels"`
	Rejected             int64   `json:"rejected_connections"`
}

// ComputeResources turns the snapshots at the start and end of a phase into
// usage rates.
func ComputeResources(start, end Snapshot) Resources {
	seconds := end.Time - start.Time
	rate := func(delta int64) float64 {
		if seconds <= 0 {
			return 0
		}
		return float64(delta) / seconds
	}
	resources := Resources{
		Containers: map[string]ContainerUsage{},
		Postgres:   map[string]PostgresUsage{},
		Redis:      map[string]RedisUsage{},
	}
	names := make([]string, 0, len(end.Containers))
	for name := range end.Containers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		after, before := end.Containers[name], start.Containers[name]
		usage := ContainerUsage{
			CPUCores:         rate(after.CPUNanoseconds-before.CPUNanoseconds) / 1e9,
			MemoryPeakBytes:  max(after.MemoryPeak, after.Memory),
			MemoryLimitBytes: after.MemoryLimit,
			Restarts:         after.Restarts - before.Restarts,
			OOMKilled:        after.OOMKilled,
		}
		if usage.MemoryLimitBytes > 0 {
			usage.MemoryRatio = float64(usage.MemoryPeakBytes) / float64(usage.MemoryLimitBytes)
		}
		resources.Containers[name] = usage
		if name == GeneratorContainer {
			continue
		}
		resources.StandCPUCores += usage.CPUCores
		if resources.Busiest == "" || usage.CPUCores > resources.Containers[resources.Busiest].CPUCores {
			resources.Busiest = name
		}
	}
	for name, after := range end.Postgres {
		before := start.Postgres[name]
		usage := PostgresUsage{
			TransactionsPerSecond: rate(after.Commits + after.Rollbacks - before.Commits - before.Rollbacks),
			InsertsPerSecond:      rate(after.Inserted - before.Inserted),
			FetchesPerSecond:      rate(after.Fetched - before.Fetched),
			Deadlocks:             after.Deadlocks - before.Deadlocks,
			TempBytes:             after.TempBytes - before.TempBytes,
			Backends:              after.Backends,
		}
		if hits, reads := after.BlocksHit-before.BlocksHit, after.BlocksRead-before.BlocksRead; hits+reads > 0 {
			usage.CacheHitRatio = float64(hits) / float64(hits+reads)
		}
		resources.Postgres[name] = usage
	}
	for name, after := range end.Redis {
		before := start.Redis[name]
		resources.Redis[name] = RedisUsage{
			CommandsPerSecond:    rate(after.Commands - before.Commands),
			InputBytesPerSecond:  rate(after.InputBytes - before.InputBytes),
			OutputBytesPerSecond: rate(after.OutputBytes - before.OutputBytes),
			UsedMemoryBytes:      after.UsedMemory,
			Clients:              after.Clients,
			PubSubChannels:       after.PubSubChannels,
			Rejected:             after.Rejected - before.Rejected,
		}
	}
	return resources
}
