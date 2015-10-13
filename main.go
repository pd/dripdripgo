package main

import (
	"errors"
	"flag"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/shenwei356/util/bytesize"
)

var licenseKey string
var debugMode bool

type Options struct {
	Drippers int
	HeapSize uint64
}

func main() {
	opts, err := load()
	if err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < opts.Drippers; i++ {
		// give a bit of breathing room between each
		<-time.Tick(time.Duration(rand.Float64()*100) * time.Millisecond)

		dripper := NewDripper(i+1, opts.HeapSize)

		wg.Add(1)
		go func() {
			dripper.Drip()
			wg.Done()
		}()
	}

	wg.Wait()
}

func load() (*Options, error) {
	var drippers int
	var heapStr string

	flag.StringVar(&licenseKey, "key", "", "License key")
	flag.IntVar(&drippers, "count", 1, "Number of drippers")
	flag.StringVar(&heapStr, "heap", "512 GB", "Heap size to report")
	flag.BoolVar(&debugMode, "debug", false, "Dump HTTP traffic")
	flag.Parse()

	if licenseKey == "" {
		return nil, errors.New("License key required")
	}

	heapSize, err := bytesize.Parse([]byte(heapStr))
	if err != nil {
		return nil, err
	}

	return &Options{
		Drippers: drippers,
		HeapSize: uint64(heapSize),
	}, nil
}
