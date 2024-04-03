package database

import (
	"fmt"
	"github.com/armon/go-metrics"
	"github.com/syndtr/goleveldb/leveldb"
	"time"
)

func NewLevelDB(path string, name string) (db *leveldb.DB, err error) {
	db, err = leveldb.OpenFile(path, nil)
	if err != nil {
		return
	}

	go meter(db, time.Second*5, name)

	return
}

func meter(db *leveldb.DB, refresh time.Duration, name string) {
	setGauge := func(metricName string, value int64) {
		metrics.SetGauge([]string{"leveldb", name, metricName}, float32(value))
	}

	fmt.Printf("metering %s\n", name)

	// Create the counters to store current and previous compaction values
	compactions := make([][]int64, 2)
	for i := 0; i < 2; i++ {
		compactions[i] = make([]int64, 4)
	}
	// Create storages for states and warning log tracer.
	var (
		errc chan error
		merr error

		stats           leveldb.DBStats
		iostats         [2]int64
		delaystats      [2]int64
		lastWritePaused time.Time
	)
	timer := time.NewTimer(refresh)
	defer timer.Stop()

	// Iterate ad infinitum and collect the stats
	for i := 1; errc == nil && merr == nil; i++ {
		// Retrieve the database stats
		// Stats method resets buffers inside therefore it's okay to just pass the struct.
		err := db.Stats(&stats)
		if err != nil {
			fmt.Println("Failed to read database stats")
			merr = err
			continue
		}
		// Iterate over all the leveldbTable rows, and accumulate the entries
		for j := 0; j < len(compactions[i%2]); j++ {
			compactions[i%2][j] = 0
		}
		compactions[i%2][0] = stats.LevelSizes.Sum()
		for _, t := range stats.LevelDurations {
			compactions[i%2][1] += t.Nanoseconds()
		}
		compactions[i%2][2] = stats.LevelRead.Sum()
		compactions[i%2][3] = stats.LevelWrite.Sum()

		// Update all the requested meters
		if i > 1 {
			setGauge("diskSize", compactions[i%2][0])
			setGauge("compactTime", compactions[i%2][1]-compactions[(i-1)%2][1])
			setGauge("compactRead", compactions[i%2][2]-compactions[(i-1)%2][2])
			setGauge("compactWrite", compactions[i%2][3]-compactions[(i-1)%2][3])
		}

		var (
			delayN   = int64(stats.WriteDelayCount)
			duration = stats.WriteDelayDuration
			paused   = stats.WritePaused
		)

		setGauge("writeDelayNMeter", delayN-delaystats[0])
		setGauge("writeDelayMeter", duration.Nanoseconds()-delaystats[1])

		// If a warning that db is performing compaction has been displayed, any subsequent
		// warnings will be withheld for one minute not to overwhelm the user.
		if paused && delayN-delaystats[0] == 0 && duration.Nanoseconds()-delaystats[1] == 0 &&
			time.Now().After(lastWritePaused.Add(time.Minute)) {
			fmt.Println("Database compacting, degraded performance")
			lastWritePaused = time.Now()
		}
		delaystats[0], delaystats[1] = delayN, duration.Nanoseconds()

		var (
			nRead  = int64(stats.IORead)
			nWrite = int64(stats.IOWrite)
		)
		setGauge("diskRead", nRead-iostats[0])
		setGauge("diskWrite", nWrite-iostats[1])
		iostats[0], iostats[1] = nRead, nWrite

		setGauge("memCompaction", int64(stats.MemComp))
		setGauge("level0Compaction", int64(stats.Level0Comp))
		setGauge("nonlevel0Compaction", int64(stats.NonLevel0Comp))
		setGauge("seekCompaction", int64(stats.SeekComp))

		for i, tables := range stats.LevelTablesCounts {
			setGauge(fmt.Sprintf("level_%d_tableCount", i), int64(tables))
		}

		// Sleep a bit, then repeat the stats collection
		select {
		//case errc = <-db.quitChan:
		//	// Quit requesting, stop hammering the database
		case <-timer.C:
			timer.Reset(refresh)
			// Timeout, gather a new set of stats
		}
	}

	//if errc == nil {
	//	errc = <-db.quitChan
	//}
	//errc <- merr
}