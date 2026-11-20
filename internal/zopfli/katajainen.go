// Package zopfli is a port of Zopfli's deflate compressor
// (github.com/google/zopfli; Copyright 2011 Google Inc., Apache License 2.0,
// see licenses/LICENSE-ZOPFLI): iterated optimal parsing under a cost model
// from the statistics of the parse before, blocks split where it saves bits,
// and block headers chosen among the ways of run-length coding them. Given
// the same input and options it writes the bytes Zopfli writes.
package zopfli

import (
	"slices"
	"sync"
)

// Length-limited Huffman code lengths by the boundary package-merge
// algorithm (katajainen.c): "A Fast and Space-Economical Algorithm for
// Length-Limited Coding", Katajainen, Moffat and Turpin.

type kNode struct {
	weight uint64
	tail   *kNode
	count  int
}

type kPool struct {
	nodes []kNode
	next  int
}

func (p *kPool) get() *kNode {
	n := &p.nodes[p.next]
	p.next++
	return n
}

func boundaryPM(lists [][2]*kNode, leaves []kNode, numSymbols int, pool *kPool, index int) {
	lastCount := lists[index][1].count
	if index == 0 && lastCount >= numSymbols {
		return
	}
	newChain := pool.get()
	oldChain := lists[index][1]
	lists[index][0] = oldChain
	lists[index][1] = newChain
	if index == 0 {
		*newChain = kNode{weight: leaves[lastCount].weight, count: lastCount + 1}
		return
	}
	sum := lists[index-1][0].weight + lists[index-1][1].weight
	if lastCount < numSymbols && sum > leaves[lastCount].weight {
		*newChain = kNode{weight: leaves[lastCount].weight, count: lastCount + 1, tail: oldChain.tail}
		return
	}
	*newChain = kNode{weight: sum, count: lastCount, tail: lists[index-1][1]}
	boundaryPM(lists, leaves, numSymbols, pool, index-1)
	boundaryPM(lists, leaves, numSymbols, pool, index-1)
}

func boundaryPMFinal(lists [][2]*kNode, leaves []kNode, numSymbols int, pool *kPool, index int) {
	lastCount := lists[index][1].count
	sum := lists[index-1][0].weight + lists[index-1][1].weight
	if lastCount < numSymbols && sum > leaves[lastCount].weight {
		newChain := &pool.nodes[pool.next]
		oldChain := lists[index][1].tail
		lists[index][1] = newChain
		newChain.count = lastCount + 1
		newChain.tail = oldChain
	} else {
		lists[index][1].tail = lists[index-1][1]
	}
}

func extractBitLengths(chain *kNode, leaves []kNode, bitLengths []uint32) {
	var counts [16]int
	end := 16
	ptr := 15
	value := uint32(1)
	for node := chain; node != nil; node = node.tail {
		end--
		counts[end] = node.count
	}
	val := counts[15]
	for ptr >= end {
		for ; val > counts[ptr-1]; val-- {
			bitLengths[leaves[val-1].count] = value
		}
		ptr--
		value++
	}
}

// kScratch is the memory of a run of the algorithm, kept for the next (the
// codes are made many times for each block, while splitting it).
type kScratch struct {
	leaves []kNode
	nodes  []kNode
	lists  [][2]*kNode
}

var kScratches = sync.Pool{New: func() any { return new(kScratch) }}

// lengthLimitedCodeLengths sets bitLengths to the lengths of a Huffman code
// for frequencies with no code longer than maxBits. It reports false if
// that cannot be done.
func lengthLimitedCodeLengths(frequencies []uint64, maxBits int, bitLengths []uint32) bool {
	k := kScratches.Get().(*kScratch)
	defer kScratches.Put(k)
	n := len(frequencies)
	clear(bitLengths[:n])
	leaves := k.leaves[:0]
	defer func() { k.leaves = leaves[:0] }()
	for i, f := range frequencies {
		if f != 0 {
			leaves = append(leaves, kNode{weight: f, count: i})
		}
	}
	numSymbols := len(leaves)
	if 1<<maxBits < numSymbols {
		return false
	}
	switch numSymbols {
	case 0:
		return true
	case 1:
		bitLengths[leaves[0].count] = 1
		return true
	case 2:
		bitLengths[leaves[0].count]++
		bitLengths[leaves[1].count]++
		return true
	}
	// The symbol index in the low 9 bits makes the order unique; Zopfli's
	// comparator returns the difference of the weights as an int.
	for i := range leaves {
		if leaves[i].weight >= 1<<(64-9) {
			return false
		}
		leaves[i].weight = leaves[i].weight<<9 | uint64(leaves[i].count)
	}
	slices.SortFunc(leaves, func(a, b kNode) int { return int(int32(a.weight - b.weight)) })
	for i := range leaves {
		leaves[i].weight >>= 9
	}
	if numSymbols-1 < maxBits {
		maxBits = numSymbols - 1
	}
	if need := maxBits * 2 * numSymbols; cap(k.nodes) < need {
		k.nodes = make([]kNode, need)
	}
	if cap(k.lists) < maxBits {
		k.lists = make([][2]*kNode, maxBits)
	}
	pool := &kPool{nodes: k.nodes[:maxBits*2*numSymbols]}
	lists := k.lists[:maxBits]
	node0, node1 := pool.get(), pool.get()
	*node0 = kNode{weight: leaves[0].weight, count: 1}
	*node1 = kNode{weight: leaves[1].weight, count: 2}
	for i := range lists {
		lists[i] = [2]*kNode{node0, node1}
	}
	runs := 2*numSymbols - 4
	for i := 0; i < runs-1; i++ {
		boundaryPM(lists, leaves, numSymbols, pool, maxBits-1)
	}
	boundaryPMFinal(lists, leaves, numSymbols, pool, maxBits-1)
	extractBitLengths(lists[maxBits-1][1], leaves, bitLengths)
	return true
}
