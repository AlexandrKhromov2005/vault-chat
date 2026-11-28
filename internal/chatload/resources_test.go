package chatload

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const startSnapshot = `{
  "time": 100,
  "containers": {
    "gateway": {"cpu_ns": 1000000000, "memory": 1, "memory_peak": 1, "memory_limit": 1000, "restarts": 0, "oom_killed": false},
    "chat": {"cpu_ns": 0, "memory": 1, "memory_peak": 1, "memory_limit": 1000, "restarts": 1, "oom_killed": false},
    "loadgen": {"cpu_ns": 0, "memory": 1, "memory_peak": 1, "memory_limit": 1000, "restarts": 0, "oom_killed": false}
  },
  "postgres": {"chat": {"xact_commit": 100, "xact_rollback": 0, "tup_inserted": 10, "tup_fetched": 0, "blks_read": 0, "blks_hit": 0, "deadlocks": 0, "numbackends": 3, "temp_bytes": 0}},
  "redis": {"redis": {"total_commands_processed": 50, "total_net_input_bytes": 0, "total_net_output_bytes": 0, "used_memory": 10, "connected_clients": 4, "pubsub_channels": 0, "rejected_connections": 0}}
}`

const endSnapshot = `{
  "time": 110,
  "containers": {
    "gateway": {"cpu_ns": 6000000000, "memory": 300, "memory_peak": 400, "memory_limit": 1000, "restarts": 0, "oom_killed": false},
    "chat": {"cpu_ns": 3000000000, "memory": 900, "memory_peak": 950, "memory_limit": 1000, "restarts": 2, "oom_killed": true},
    "loadgen": {"cpu_ns": 20000000000, "memory": 1, "memory_peak": 1, "memory_limit": 1000, "restarts": 0, "oom_killed": false}
  },
  "postgres": {"chat": {"xact_commit": 1100, "xact_rollback": 100, "tup_inserted": 410, "tup_fetched": 50, "blks_read": 10, "blks_hit": 90, "deadlocks": 1, "numbackends": 7, "temp_bytes": 0}},
  "redis": {"redis": {"total_commands_processed": 1050, "total_net_input_bytes": 2000, "total_net_output_bytes": 4000, "used_memory": 20, "connected_clients": 5, "pubsub_channels": 120, "rejected_connections": 0}}
}`

func TestResourcesComputeRatesBetweenSnapshots(t *testing.T) {
	var start, end Snapshot
	require.NoError(t, json.Unmarshal([]byte(startSnapshot), &start))
	require.NoError(t, json.Unmarshal([]byte(endSnapshot), &end))
	resources := ComputeResources(start, end)

	gateway := resources.Containers["gateway"]
	require.InDelta(t, 0.5, gateway.CPUCores, 1e-9)
	require.Equal(t, int64(400), gateway.MemoryPeakBytes)
	require.InDelta(t, 0.4, gateway.MemoryRatio, 1e-9)
	chat := resources.Containers["chat"]
	require.Equal(t, 1, chat.Restarts)
	require.True(t, chat.OOMKilled)
	require.InDelta(t, 0.8, resources.StandCPUCores, 1e-9, "the generator is not part of the stand")
	require.Equal(t, "gateway", resources.Busiest)

	pg := resources.Postgres["chat"]
	require.InDelta(t, 110, pg.TransactionsPerSecond, 1e-9)
	require.InDelta(t, 40, pg.InsertsPerSecond, 1e-9)
	require.InDelta(t, 0.9, pg.CacheHitRatio, 1e-9)
	require.Equal(t, int64(1), pg.Deadlocks)
	require.Equal(t, int64(7), pg.Backends)

	redis := resources.Redis["redis"]
	require.InDelta(t, 100, redis.CommandsPerSecond, 1e-9)
	require.InDelta(t, 200, redis.InputBytesPerSecond, 1e-9)
	require.InDelta(t, 400, redis.OutputBytesPerSecond, 1e-9)
	require.Equal(t, int64(120), redis.PubSubChannels)
}

func TestResourcesOfAnEmptyIntervalAreZero(t *testing.T) {
	var snapshot Snapshot
	require.NoError(t, json.Unmarshal([]byte(startSnapshot), &snapshot))
	resources := ComputeResources(snapshot, snapshot)
	require.Zero(t, resources.Containers["gateway"].CPUCores)
	require.Zero(t, resources.Postgres["chat"].TransactionsPerSecond)
}
