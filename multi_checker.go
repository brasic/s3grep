package main

import (
	"fmt"
	"launchpad.net/goamz/s3"
	"sync/atomic"
)

var workUnit = 950

// Takes pre-sanitized id strings and groups them into a map keyed by the
// number of digits they contain.
func groupByDigitLength(ids []string) (groups map[int][]string) {
	groups = make(map[int][]string)
	for i, _ := range ids {
		l := len(ids[i])
		if _, ok := groups[l]; ok {
			groups[l] = append(groups[l], ids[i])
		} else {
			groups[l] = []string{ids[i]}
		}
	}
	return
}

// Breaks an input slice of ids into a slice of smaller work-unit-size slices.
func split(ids []string) (workBits [][]string) {
	llen := len(ids)
	if llen < workUnit {
		return [][]string{ids}
	}
	start := 0
	end := workUnit
	for {
		if start >= llen {
			break
		}
		workBits = append(workBits, ids[start:end])
		start = end
		end = lesserOf(llen, end+workUnit)
	}
	return
}

func lesserOf(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func checkBulkKeysParallel(keys []string) {
	input := make(chan []string)
	output := make(chan []s3.Key)
	quit := make(chan int)
	jobs := split(keys)
	startBulkWorkers(input, output, quit)
	for i, _ := range jobs {
		input <- jobs[i]
	}
	notifyBulkDone(input)
	waitForAcks(quit)
}

func checkBulkWorker(inbox <-chan []string, outbox chan<- []s3.Key, quit chan<- int) {
	for {
		ids := <-inbox
		if len(ids) == 0 {
			quit <- 1
			break
		}
		found := make(map[string]bool)
		for i, _ := range ids {
			found[format(ids[i])] = false
		}
		allFiles := []s3.Key{}
		debug("lex. earliest key is", ids[0])
		debug("lex. last key is", ids[len(ids)-1])
		firstSearchKey := predecessor(ids[0])
		lastSearchKey := ids[len(ids)-1]
		path := fmt.Sprintf("%s/docs/", employerId)
		nextMarker := fmt.Sprintf("%s%s.pdf", path, firstSearchKey)
		for {
			debug("querying for prefix", path, "starting with", nextMarker)
			resp, err := bucket.List(path, "/", nextMarker, maxKeysPerCall)
			atomic.AddInt64(&apiCalls, 1)
			if err != nil {
				panic(err)
			}
			if len(resp.Contents) < 1 {
				debug("got no responses.")
				break
			}
			for i, _ := range resp.Contents {
				found[resp.Contents[i].Key] = true
			}
			allFiles = append(allFiles, resp.Contents...)
			nextMarker = resp.Contents[len(resp.Contents)-1].Key
			debug("got", len(resp.Contents), "keys, ending with", nextMarker, ". Currently have", len(allFiles), "total")
			if gt(nextMarker, lastSearchKey) {
				debug("Done since", nextMarker, "is > than our sample's largest member,", lastSearchKey)
				break
			}
			if !resp.IsTruncated {
				debug("Ending GET loop since isTruncated == false")
				break
			}
		}

		debug("Done getting", len(allFiles), "keys")
		outbox <- allFiles
	}
}

func startBulkWorkers(input chan []string, output chan []s3.Key, quit chan int) {
	for i := 0; i < workerCount; i++ {
		go checkBulkWorker(input, output, quit)
	}
}

func notifyBulkDone(work chan []string) {
	for i := 0; i < workerCount; i++ {
		work <- []string{}
	}
}