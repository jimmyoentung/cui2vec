// package cui2vec implements utilities for dealing with cui2vec embeddings and mapping cuis to text.
package cui2vec

import (
	"io"
	"bufio"
	"sort"
	"encoding/csv"
	"bytes"
	"strconv"
	"sync"
	"runtime"
	"fmt"
	"regexp"
	"github.com/go-errors/errors"
)

// Embeddings is a complete cui2vec file loaded into memory.
type Embeddings map[string][]float64

// Concept is a CUI that has a similarity score in relation to a target CUI.
type Concept struct {
	CUI   string
	Value float64
}

// LoadModel a cui2vec pre-trained model into memory.
// The pre-trained file from:
// 	https://arxiv.org/pdf/1804.01486.pdf
// which was downloaded from:
//	https://figshare.com/s/00d69861786cd0156d81
// is a csv file. The skipFirst parameter determines if the first line of the file should be skipped.
func LoadModel(r io.Reader, skipFirst bool) (Embeddings, error) {
	scanner := bufio.NewScanner(r)
	if skipFirst {
		scanner.Scan()
	}

	concurrency := runtime.NumCPU()
	var mu sync.Mutex
	queue := make(chan string)
	complete := make(chan bool)
	vector := make(Embeddings)

	// Read the pre-trained vector file line by line.
	go func() {
		for scanner.Scan() {
			queue <- scanner.Text()
		}
		close(queue)
	}()

	for i := 0; i < concurrency; i++ {
		go func(q chan string, complete chan bool) {
			for b := range q {
				// Use a csv parser to read the line.
				line, err := csv.NewReader(bytes.NewBufferString(b)).Read()
				if err != nil {
					panic(err)
				}
				if len(line) > 0 {
					cui := line[0]
					vec := make([]float64, len(line))
					for i := 1; i < len(line); i++ {
						// The features come in as strings and must be parsed.
						vec[i], err = strconv.ParseFloat(line[i], 64)
						if err != nil {
							fmt.Println(len(line), line)
							panic(err)
						}
					}
					mu.Lock()
					vector[cui] = vec
					mu.Unlock()
				}
			}
			complete <- true
		}(queue, complete)
	}

	// Wait until the last goroutine has read from the semaphore.
	for i := 0; i < concurrency; i++ {
		<-complete
	}
	return vector, nil
}

// Similar computes cuis that a similar to an input CUI. The distance function used is cosine similarity. The CUIs are
// then run through softmax and sorted.
func (v Embeddings) Similar(cui string) ([]Concept, error) {
	vec := v[cui]
	var cuis []Concept
	i := 0

	concurrency := runtime.NumCPU() * 2
	sem := make(chan bool, concurrency)
	var mu sync.Mutex

	// Compute the cosine similarity for each value.
	for vectorCui, vectorVector := range v {
		sem <- true
		go func(c string, f []float64) {
			defer func() { <-sem }()
			if c != cui {
				sim, err := cosine(vec, f)
				if err != nil {
					return
				}

				if len(c) == 0 {
					return
				}

				mu.Lock()
				cuis = append(cuis, Concept{
					CUI:   c,
					Value: sim,
				})
				i++
				mu.Unlock()
			}
		}(vectorCui, vectorVector)
	}

	// Wait until the last goroutine has read from the semaphore.
	for i := 0; i < cap(sem); i++ {
		sem <- true
	}

	// Softmax the values.
	cuis = softmax(cuis)

	// Sort the values.
	sort.Slice(cuis, func(i, j int) bool {
		return cuis[i].Value > cuis[j].Value
	})

	return cuis, nil
}

// CUI2Int converts a string CUI into an integer.
func CUI2Int(cui string) (int, error) {
	re, _ := regexp.Compile("C[0]+(?P<CUI>[0-9]+)")
	m := re.FindAllStringSubmatch(cui, -1)
	if len(m) != 1 {
		return 0, errors.New(fmt.Sprintf("%s is not a cui", cui))
	}
	v, err := strconv.Atoi(m[0][1])
	if err != nil {
		return 0, err
	}
	return v, nil
}

// Int2CUI converts an integer value to a CUI.
func Int2CUI(val int) string {
	cui := strconv.Itoa(val)
	for len(cui) < 7 {
		cui = "0" + cui
	}
	return "C" + cui
}