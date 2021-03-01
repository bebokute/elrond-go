package health

import (
	"fmt"
	"os"
	"path"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/ElrondNetwork/elrond-go/core"
)

var _ record = (*memoryUsageRecord)(nil)

type memoryUsageRecord struct {
	stats        runtime.MemStats
	timestamp    time.Time
	parentFolder string
}

func newMemoryUsageRecord(stats runtime.MemStats, timestamp time.Time, parentFolder string) *memoryUsageRecord {
	return &memoryUsageRecord{
		stats:        stats,
		timestamp:    timestamp,
		parentFolder: parentFolder,
	}
}

func (record *memoryUsageRecord) save() error {
	filename := record.getFilename()
	file, err := os.Create(filename)
	if err != nil {
		return err
	}

	log.Debug("memoryUsageRecord.save()", "file", filename)

	err = pprof.WriteHeapProfile(file)
	if err != nil {
		return err
	}

	return file.Close()
}

func (record *memoryUsageRecord) getFilename() string {
	timestamp := record.timestamp.Format("20060102150405")
	inUse := core.ConvertBytes(record.stats.HeapInuse)
	inUse = strings.ReplaceAll(inUse, " ", "_")
	inUse = strings.ReplaceAll(inUse, ".", "_")
	filename := fmt.Sprintf("mem__%s__%s.pprof", timestamp, inUse)
	return path.Join(record.parentFolder, filename)
}

func (record *memoryUsageRecord) delete() error {
	return os.Remove(record.getFilename())
}

func (record *memoryUsageRecord) isMoreImportantThan(otherRecord record) bool {
	asMemoryUsageRecord, ok := otherRecord.(*memoryUsageRecord)
	if !ok {
		return false
	}

	return record.stats.HeapInuse > asMemoryUsageRecord.stats.HeapInuse
}
