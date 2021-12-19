package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/nddq/presto/fingerprint"
)

type Result struct {
	filename         string
	highestMatchRate float64
}

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintf(os.Stderr, "Missing args\n")
		os.Exit(1)
	}
	audioDirectory := os.Args[1]
	sampleName := os.Args[2]
	winSize, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not a valid window size\n")
		os.Exit(1)
	}
	hopSize, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Not a valid hop size\n")
		os.Exit(1)
	}
	var winFunc string
	if len(os.Args) == 6 {
		winFunc = os.Args[5]
	} else {
		winFunc = ""
	}

	var wg sync.WaitGroup
	c := make(chan *Result)
	fingerprintMap := make(map[string][][]int)
	var mapMu sync.Mutex

	files, err := ioutil.ReadDir(audioDirectory) // read all audio files in directory
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		fn := file.Name()
		wg.Add(1)
		go func() { // Fingerprinting multiple audio file concurrently
			defer wg.Done()

			fp := fingerprint.Fingerprint(audioDirectory+fn, winSize, hopSize, winFunc, false)
			mapMu.Lock()
			defer mapMu.Unlock()
			fingerprintMap[fn] = fp
		}()
	}
	wg.Wait() // wait for all fingerprinting process to complete
	fmt.Printf("Done fingerprinting all audio files. Moving to compare\n")
	sampleFP := fingerprint.Fingerprint(sampleName, winSize, hopSize, winFunc, false) // fingerprinting input sample
	for k, v := range fingerprintMap {
		filename := k
		fp := v
		wg.Add(1)
		go func() {
			defer wg.Done() // find matches from audio files concurrently
			matchRate := fingerprint.GetHighestMatchRate(sampleName, sampleFP, filename, fp)
			fmt.Printf("%s : %v\n", filename, matchRate)
			c <- &Result{filename: filename, highestMatchRate: matchRate}
		}()
	}
	go func() { // wait for all comparison to finished
		wg.Wait()
		close(c)
	}()
	closestMatch := &Result{highestMatchRate: 0}
	for res := range c { // find audio file that has the highest match rate
		if res.highestMatchRate > closestMatch.highestMatchRate {
			closestMatch = res
		}
	}
	fmt.Printf("Closest match is %s with highest match rate of %v\n", closestMatch.filename, closestMatch.highestMatchRate)
}
