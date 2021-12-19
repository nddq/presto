package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"sync"

	"github.com/nddq/audioRecognition/fingerprint"
)

type Result struct {
	filename         string
	highestMatchRate float64
}

func main() {
	var wg sync.WaitGroup
	c := make(chan *Result)
	fingerprintMap := make(map[string][][]int)
	var mapMu sync.Mutex

	files, err := ioutil.ReadDir("./audio") // read all audio files in directory
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		fn := file.Name()
		wg.Add(1)
		go func() { // Fingerprinting multiple audio file concurrently
			defer wg.Done()
			fp := fingerprint.Fingerprint("./audio/" + fn)
			mapMu.Lock()
			defer mapMu.Unlock()
			fingerprintMap[fn] = fp
		}()
	}
	wg.Wait()
	fmt.Printf("Done fingerprinting all audio files. Moving to compare\n")
	sampleFP := fingerprint.Fingerprint("sample1Shorten.wav") // fingerprinting input sample
	for k, v := range fingerprintMap {
		filename := k
		fp := v
		wg.Add(1)
		go func() {
			defer wg.Done() // find matches from audio files concurrently
			matchRate := fingerprint.GetHighestMatchRate("sample1Shorten.wav", sampleFP, filename, fp)
			fmt.Printf("%s : %v\n", filename, matchRate)
			c <- &Result{filename: filename, highestMatchRate: matchRate}
		}()
	}
	go func() {
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
