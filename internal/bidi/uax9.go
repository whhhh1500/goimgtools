// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Unicode Bidirectional Algorithm for a paragraph, from
// golang.org/x/text/unicode/bidi (core.go, bracket.go), itself a port of the
// reference implementation of Unicode
// (https://www.unicode.org/Public/PROGRAMS/BidiReferenceJava/), which
// golang.org/x/text checks against BidiTest.txt and BidiCharacterTest.txt.
// Changed here: no checks of the input, which is made right by the caller,
// and so no panics; the bracket pairs sorted once, not at every closing
// bracket; a slice for the stack of openers. Its own entry points (whose
// bracket values never pair) are not used.

package bidi

import (
	"slices"

	xbidi "golang.org/x/text/unicode/bidi"
)

type Class = xbidi.Class

const (
	L   = xbidi.L
	R   = xbidi.R
	EN  = xbidi.EN
	ES  = xbidi.ES
	ET  = xbidi.ET
	AN  = xbidi.AN
	CS  = xbidi.CS
	B   = xbidi.B
	S   = xbidi.S
	WS  = xbidi.WS
	ON  = xbidi.ON
	BN  = xbidi.BN
	NSM = xbidi.NSM
	AL  = xbidi.AL
	LRO = xbidi.LRO
	RLO = xbidi.RLO
	LRE = xbidi.LRE
	RLE = xbidi.RLE
	PDF = xbidi.PDF
	LRI = xbidi.LRI
	RLI = xbidi.RLI
	FSI = xbidi.FSI
	PDI = xbidi.PDI

	unknownClass = ^Class(0)
)

// level is the embedding level of a character: even left to right, odd
// right to left; -1 is undefined.
type level int8

const implicitLevel level = -1

func in(c Class, set ...Class) bool {
	for _, s := range set {
		if c == s {
			return true
		}
	}
	return false
}

type bracketType byte

const (
	bpNone bracketType = iota
	bpOpen
	bpClose
)

// A paragraph is the state of the algorithm for one paragraph.
type paragraph struct {
	initialTypes []Class

	// For the paired brackets of rule N0: each character's bracket type,
	// and for brackets the opening bracket of their pair (after
	// normalization), so that the two of a pair have the same.
	pairTypes  []bracketType
	pairValues []rune

	embeddingLevel level

	resultTypes  []Class
	resultLevels []level

	// The matching PDI of each isolate initiator (the length of the text
	// for none; -1 for other characters), and the matching initiator of
	// each PDI (-1 for none and for other characters).
	matchingPDI              []int
	matchingIsolateInitiator []int
}

// newParagraph runs the algorithm on a paragraph but for the rules of
// lines (L1, L2). types must not be empty and hold B only at the end;
// lvl is 0, 1 or implicitLevel (rules P2, P3).
func newParagraph(types []Class, pairTypes []bracketType, pairValues []rune, lvl level) *paragraph {
	p := &paragraph{
		initialTypes:   append([]Class(nil), types...),
		embeddingLevel: lvl,
		pairTypes:      pairTypes,
		pairValues:     pairValues,
		resultTypes:    append([]Class(nil), types...),
	}
	p.run()
	return p
}

func (p *paragraph) Len() int { return len(p.initialTypes) }

func (p *paragraph) run() {
	p.determineMatchingIsolates()
	// Rules P2, P3.
	if p.embeddingLevel == implicitLevel {
		p.embeddingLevel = p.determineParagraphEmbeddingLevel(0, p.Len())
	}
	p.resultLevels = make([]level, p.Len())
	setLevels(p.resultLevels, p.embeddingLevel)
	// Rules X1-X8.
	p.determineExplicitEmbeddingLevels()
	// Rule X9: the embeddings, overrides, PDFs and BNs are left out of the
	// isolating run sequences. Rule X10: the rest one sequence at a time.
	for _, seq := range p.determineIsolatingRunSequences() {
		seq.resolveWeakTypes()      // W1-W7
		resolvePairedBrackets(seq)  // N0
		seq.resolveNeutralTypes()   // N1, N2
		seq.resolveImplicitLevels() // I1, I2
		seq.applyLevelsAndTypes()
	}
	p.assignLevelsToCharactersRemovedByX9()
}

// determineMatchingIsolates finds the matching PDI of each isolate
// initiator and the other way (BD9).
func (p *paragraph) determineMatchingIsolates() {
	p.matchingPDI = make([]int, p.Len())
	p.matchingIsolateInitiator = make([]int, p.Len())
	for i := range p.matchingIsolateInitiator {
		p.matchingIsolateInitiator[i] = -1
	}
	var open []int
	for i := range p.matchingPDI {
		p.matchingPDI[i] = -1
		switch t := p.resultTypes[i]; {
		case in(t, LRI, RLI, FSI):
			open = append(open, i)
		case t == PDI && len(open) > 0:
			j := open[len(open)-1]
			open = open[:len(open)-1]
			p.matchingPDI[j] = i
			p.matchingIsolateInitiator[i] = j
		}
	}
	for _, i := range open {
		p.matchingPDI[i] = p.Len()
	}
}

// determineParagraphEmbeddingLevel is the direction of the text from start
// to end by rules P2, P3 (and for X5c, whether an FSI is an LRI or an RLI).
func (p *paragraph) determineParagraphEmbeddingLevel(start, end int) level {
	strongType := unknownClass
	for i := start; i < end; i++ {
		if t := p.resultTypes[i]; in(t, L, AL, R) {
			strongType = t
			break
		} else if in(t, FSI, LRI, RLI) {
			i = p.matchingPDI[i] // past the matching PDI
		}
	}
	switch strongType {
	case unknownClass, L:
		return 0
	default: // AL, R
		return 1
	}
}

const maxDepth = 125

// The stack of embedding levels and override and isolate statuses.
type directionalStatusStack struct {
	stackCounter        int
	embeddingLevelStack [maxDepth + 1]level
	overrideStatusStack [maxDepth + 1]Class
	isolateStatusStack  [maxDepth + 1]bool
}

func (s *directionalStatusStack) empty()     { s.stackCounter = 0 }
func (s *directionalStatusStack) pop()       { s.stackCounter-- }
func (s *directionalStatusStack) depth() int { return s.stackCounter }

func (s *directionalStatusStack) push(level level, overrideStatus Class, isolateStatus bool) {
	s.embeddingLevelStack[s.stackCounter] = level
	s.overrideStatusStack[s.stackCounter] = overrideStatus
	s.isolateStatusStack[s.stackCounter] = isolateStatus
	s.stackCounter++
}

func (s *directionalStatusStack) lastEmbeddingLevel() level {
	return s.embeddingLevelStack[s.stackCounter-1]
}

func (s *directionalStatusStack) lastDirectionalOverrideStatus() Class {
	return s.overrideStatusStack[s.stackCounter-1]
}

func (s *directionalStatusStack) lastDirectionalIsolateStatus() bool {
	return s.isolateStatusStack[s.stackCounter-1]
}

// determineExplicitEmbeddingLevels applies rules X1-X8.
func (p *paragraph) determineExplicitEmbeddingLevels() {
	var stack directionalStatusStack
	var overflowIsolateCount, overflowEmbeddingCount, validIsolateCount int
	// Rule X1.
	stack.push(p.embeddingLevel, ON, false)
	for i, t := range p.resultTypes {
		switch t {
		case RLE, LRE, RLO, LRO, RLI, LRI, FSI: // X2-X5c
			isIsolate := in(t, RLI, LRI, FSI)
			isRTL := in(t, RLE, RLO, RLI)
			if t == FSI {
				isRTL = p.determineParagraphEmbeddingLevel(i+1, p.matchingPDI[i]) == 1
			}
			if isIsolate {
				p.resultLevels[i] = stack.lastEmbeddingLevel()
				if stack.lastDirectionalOverrideStatus() != ON {
					p.resultTypes[i] = stack.lastDirectionalOverrideStatus()
				}
			}
			var newLevel level
			if isRTL {
				newLevel = (stack.lastEmbeddingLevel() + 1) | 1 // least greater odd
			} else {
				newLevel = (stack.lastEmbeddingLevel() + 2) &^ 1 // least greater even
			}
			if newLevel <= maxDepth && overflowIsolateCount == 0 && overflowEmbeddingCount == 0 {
				if isIsolate {
					validIsolateCount++
				}
				switch t {
				case LRO:
					stack.push(newLevel, L, isIsolate)
				case RLO:
					stack.push(newLevel, R, isIsolate)
				default:
					stack.push(newLevel, ON, isIsolate)
				}
				if !isIsolate {
					p.resultLevels[i] = newLevel
				}
			} else if isIsolate {
				overflowIsolateCount++
			} else if overflowIsolateCount == 0 {
				overflowEmbeddingCount++
			}
		case PDI: // X6a
			if overflowIsolateCount > 0 {
				overflowIsolateCount--
			} else if validIsolateCount > 0 {
				overflowEmbeddingCount = 0
				for !stack.lastDirectionalIsolateStatus() {
					stack.pop()
				}
				stack.pop()
				validIsolateCount--
			}
			p.resultLevels[i] = stack.lastEmbeddingLevel()
		case PDF: // X7
			p.resultLevels[i] = stack.lastEmbeddingLevel()
			if overflowIsolateCount > 0 {
				// nothing
			} else if overflowEmbeddingCount > 0 {
				overflowEmbeddingCount--
			} else if !stack.lastDirectionalIsolateStatus() && stack.depth() >= 2 {
				stack.pop()
			}
		case B: // X8; B is only at the end
			stack.empty()
			overflowIsolateCount, overflowEmbeddingCount, validIsolateCount = 0, 0, 0
			p.resultLevels[i] = p.embeddingLevel
		default:
			p.resultLevels[i] = stack.lastEmbeddingLevel()
			if stack.lastDirectionalOverrideStatus() != ON {
				p.resultTypes[i] = stack.lastDirectionalOverrideStatus()
			}
		}
	}
}

type isolatingRunSequence struct {
	p *paragraph

	indexes []int // into the paragraph

	types          []Class
	resolvedLevels []level
	level          level
	sos, eos       Class
}

func (s *isolatingRunSequence) Len() int { return len(s.indexes) }

// isolatingRunSequence makes the sequence of indexes, with its sos and eos
// (rule X10).
func (p *paragraph) isolatingRunSequence(indexes []int) *isolatingRunSequence {
	length := len(indexes)
	types := make([]Class, length)
	for i, x := range indexes {
		types[i] = p.resultTypes[x]
	}
	prevChar := indexes[0] - 1
	for prevChar >= 0 && isRemovedByX9(p.initialTypes[prevChar]) {
		prevChar--
	}
	prevLevel := p.embeddingLevel
	if prevChar >= 0 {
		prevLevel = p.resultLevels[prevChar]
	}
	var succLevel level
	if in(types[length-1], LRI, RLI, FSI) {
		succLevel = p.embeddingLevel
	} else {
		limit := indexes[length-1] + 1
		for limit < p.Len() && isRemovedByX9(p.initialTypes[limit]) {
			limit++
		}
		succLevel = p.embeddingLevel
		if limit < p.Len() {
			succLevel = p.resultLevels[limit]
		}
	}
	lvl := p.resultLevels[indexes[0]]
	return &isolatingRunSequence{
		p:       p,
		indexes: indexes,
		types:   types,
		level:   lvl,
		sos:     typeForLevel(max(prevLevel, lvl)),
		eos:     typeForLevel(max(succLevel, lvl)),
	}
}

// resolveWeakTypes applies rules W1-W7.
func (s *isolatingRunSequence) resolveWeakTypes() {
	// W1: marks take the type of what is before them.
	precedingCharacterType := s.sos
	for i, t := range s.types {
		if t == NSM {
			s.types[i] = precedingCharacterType
		} else {
			precedingCharacterType = t
		}
	}
	// W2.
	lastStrong := unknownClass
	for i, t := range s.types {
		switch {
		case in(t, L, R, AL):
			lastStrong = t
		case t == EN && lastStrong == AL:
			s.types[i] = AN
		}
	}
	// W3.
	for i, t := range s.types {
		if t == AL {
			s.types[i] = R
		}
	}
	// W4 (a change can only reach the next value, and only one that is not
	// a separator).
	for i := 1; i < s.Len()-1; i++ {
		t := s.types[i]
		if t == ES || t == CS {
			prevSepType := s.types[i-1]
			succSepType := s.types[i+1]
			if prevSepType == EN && succSepType == EN {
				s.types[i] = EN
			} else if s.types[i] == CS && prevSepType == AN && succSepType == AN {
				s.types[i] = AN
			}
		}
	}
	// W5.
	for i := 0; i < len(s.types); i++ {
		if s.types[i] != ET {
			continue
		}
		runStart := i
		runEnd := s.findRunLimit(runStart, ET)
		t := s.sos
		if runStart > 0 {
			t = s.types[runStart-1]
		}
		if t != EN {
			t = s.eos
			if runEnd < len(s.types) {
				t = s.types[runEnd]
			}
		}
		if t == EN {
			setTypes(s.types[runStart:runEnd], EN)
		}
		i = runEnd
	}
	// W6.
	for i, t := range s.types {
		if in(t, ES, ET, CS) {
			s.types[i] = ON
		}
	}
	// W7.
	prevStrongType := s.sos
	for i, t := range s.types {
		switch {
		case t == L || t == R:
			prevStrongType = t
		case t == EN && prevStrongType == L:
			s.types[i] = L
		}
	}
}

// resolveNeutralTypes applies rules N1, N2.
func (s *isolatingRunSequence) resolveNeutralTypes() {
	for i, t := range s.types {
		switch t {
		case WS, ON, B, S, RLI, LRI, FSI, PDI:
			runStart := i
			runEnd := s.findRunLimit(runStart, B, S, WS, ON, RLI, LRI, FSI, PDI)
			var leadType, trailType Class
			if runStart == 0 {
				leadType = s.sos
			} else {
				leadType = s.types[runStart-1]
				if in(leadType, AN, EN) {
					leadType = R
				}
			}
			if runEnd == len(s.types) {
				trailType = s.eos
			} else {
				trailType = s.types[runEnd]
				if in(trailType, AN, EN) {
					trailType = R
				}
			}
			var resolvedType Class
			if leadType == trailType {
				resolvedType = leadType // N1
			} else {
				resolvedType = typeForLevel(s.level) // N2: the run's level
			}
			setTypes(s.types[runStart:runEnd], resolvedType)
			i = runEnd
		}
	}
}

func setLevels(levels []level, newLevel level) {
	for i := range levels {
		levels[i] = newLevel
	}
}

func setTypes(types []Class, newType Class) {
	for i := range types {
		types[i] = newType
	}
}

// resolveImplicitLevels applies rules I1, I2.
func (s *isolatingRunSequence) resolveImplicitLevels() {
	s.resolvedLevels = make([]level, len(s.types))
	setLevels(s.resolvedLevels, s.level)
	if s.level&1 == 0 {
		for i, t := range s.types {
			switch t {
			case L:
			case R:
				s.resolvedLevels[i]++
			default: // AN, EN
				s.resolvedLevels[i] += 2
			}
		}
	} else {
		for i, t := range s.types {
			if t != R {
				s.resolvedLevels[i]++
			}
		}
	}
}

func (s *isolatingRunSequence) applyLevelsAndTypes() {
	for i, x := range s.indexes {
		s.p.resultTypes[x] = s.types[i]
		s.p.resultLevels[x] = s.resolvedLevels[i]
	}
}

// findRunLimit is the end of the run of the types in validSet from index.
func (s *isolatingRunSequence) findRunLimit(index int, validSet ...Class) int {
	for ; index < len(s.types); index++ {
		if !in(s.types[index], validSet...) {
			return index
		}
	}
	return len(s.types)
}

// determineLevelRuns gives the level runs, as indexes into the text, the
// characters removed by rule X9 left out.
func (p *paragraph) determineLevelRuns() [][]int {
	var run []int
	var allRuns [][]int
	currentLevel := implicitLevel
	for i := range p.initialTypes {
		if isRemovedByX9(p.initialTypes[i]) {
			continue
		}
		if p.resultLevels[i] != currentLevel {
			if currentLevel >= 0 {
				allRuns = append(allRuns, run)
				run = nil
			}
			currentLevel = p.resultLevels[i]
		}
		run = append(run, i)
	}
	if len(run) > 0 {
		allRuns = append(allRuns, run)
	}
	return allRuns
}

// determineIsolatingRunSequences gives the isolating run sequences (BD13).
func (p *paragraph) determineIsolatingRunSequences() []*isolatingRunSequence {
	levelRuns := p.determineLevelRuns()
	runForCharacter := make([]int, p.Len())
	for i, run := range levelRuns {
		for _, index := range run {
			runForCharacter[index] = i
		}
	}
	var sequences []*isolatingRunSequence
	for _, run := range levelRuns {
		first := run[0]
		if p.initialTypes[first] == PDI && p.matchingIsolateInitiator[first] != -1 {
			continue // part of the sequence of its initiator
		}
		var current []int
		for {
			current = append(current, run...)
			last := current[len(current)-1]
			if in(p.initialTypes[last], LRI, RLI, FSI) && p.matchingPDI[last] != p.Len() {
				run = levelRuns[runForCharacter[p.matchingPDI[last]]]
			} else {
				break
			}
		}
		sequences = append(sequences, p.isolatingRunSequence(current))
	}
	return sequences
}

// assignLevelsToCharactersRemovedByX9 gives the characters left out by
// rule X9 the level of the one before them, so that they part no runs.
func (p *paragraph) assignLevelsToCharactersRemovedByX9() {
	for i, t := range p.initialTypes {
		if in(t, LRE, RLE, LRO, RLO, PDF, BN) {
			p.resultTypes[i] = t
			p.resultLevels[i] = -1
		}
	}
	if p.resultLevels[0] == -1 {
		p.resultLevels[0] = p.embeddingLevel
	}
	for i := 1; i < len(p.initialTypes); i++ {
		if p.resultLevels[i] == -1 {
			p.resultLevels[i] = p.resultLevels[i-1]
		}
	}
}

// isWhitespace reports whether c counts as white space for rule L1 (the
// format characters too).
func isWhitespace(c Class) bool {
	switch c {
	case LRE, RLE, LRO, RLO, PDF, LRI, RLI, FSI, PDI, BN, WS:
		return true
	}
	return false
}

func isRemovedByX9(c Class) bool {
	switch c {
	case LRE, RLE, LRO, RLO, PDF, BN:
		return true
	}
	return false
}

func typeForLevel(l level) Class {
	if l&1 == 0 {
		return L
	}
	return R
}

// Rule N0, the paired brackets (BD14-BD16), from the reference
// implementation's BidiPBAReference.java.

type bracketPair struct {
	opener int
	closer int
}

func resolvePairedBrackets(s *isolatingRunSequence) {
	p := bracketPairer{
		sos:              s.sos,
		codesIsolatedRun: s.types,
		indexes:          s.indexes,
	}
	dirEmbed := L
	if s.level&1 != 0 {
		dirEmbed = R
	}
	p.locateBrackets(s.p.pairTypes, s.p.pairValues)
	p.resolveBrackets(dirEmbed, s.p.initialTypes)
}

type bracketPairer struct {
	sos Class

	// The positions of the opening brackets not yet closed, the latest
	// last.
	openers []int

	// The bracket pairs, in the order of their opening brackets.
	pairPositions []bracketPair

	codesIsolatedRun []Class
	indexes          []int
}

func (p *bracketPairer) matchOpener(pairValues []rune, opener, closer int) bool {
	return pairValues[p.indexes[opener]] == pairValues[p.indexes[closer]]
}

const maxPairingDepth = 63

// locateBrackets finds the bracket pairs (BD16).
func (p *bracketPairer) locateBrackets(pairTypes []bracketType, pairValues []rune) {
	defer func() {
		slices.SortFunc(p.pairPositions, func(a, b bracketPair) int { return a.opener - b.opener })
	}()
	for i, index := range p.indexes {
		if pairTypes[index] == bpNone || p.codesIsolatedRun[i] != ON {
			continue
		}
		switch pairTypes[index] {
		case bpOpen:
			if len(p.openers) == maxPairingDepth {
				p.openers = p.openers[:0]
				return
			}
			p.openers = append(p.openers, i)
		case bpClose:
			// The latest opener that matches, and those after it, go; a
			// closing bracket that matches none is left.
			for k := len(p.openers) - 1; k >= 0; k-- {
				if p.matchOpener(pairValues, p.openers[k], i) {
					p.pairPositions = append(p.pairPositions, bracketPair{p.openers[k], i})
					p.openers = p.openers[:k]
					break
				}
			}
		}
	}
}

// getStrongTypeN0 is the strong type of a character for rule N0, numbers
// as R; ON for none.
func (p *bracketPairer) getStrongTypeN0(index int) Class {
	switch p.codesIsolatedRun[index] {
	case EN, AN, AL, R:
		return R
	case L:
		return L
	default:
		return ON
	}
}

// classifyPairContent is the embedding direction if a strong type of it is
// inside the pair, else the opposite one if that is, else ON.
func (p *bracketPairer) classifyPairContent(loc bracketPair, dirEmbed Class) Class {
	dirOpposite := ON
	for i := loc.opener + 1; i < loc.closer; i++ {
		dir := p.getStrongTypeN0(i)
		if dir == ON {
			continue
		}
		if dir == dirEmbed {
			return dir
		}
		dirOpposite = dir
	}
	return dirOpposite
}

// classBeforePair is the first strong type before the pair, or sos.
func (p *bracketPairer) classBeforePair(loc bracketPair) Class {
	for i := loc.opener - 1; i >= 0; i-- {
		if dir := p.getStrongTypeN0(i); dir != ON {
			return dir
		}
	}
	return p.sos
}

// assignBracketType applies rule N0 to one pair.
func (p *bracketPairer) assignBracketType(loc bracketPair, dirEmbed Class, initialTypes []Class) {
	dirPair := p.classifyPairContent(loc, dirEmbed)
	if dirPair == ON {
		return // N0 d
	}
	if dirPair != dirEmbed { // N0 c
		dirPair = p.classBeforePair(loc)
		if dirPair == dirEmbed || dirPair == ON {
			dirPair = dirEmbed
		}
	}
	p.setBracketsToType(loc, dirPair, initialTypes)
}

func (p *bracketPairer) setBracketsToType(loc bracketPair, dirPair Class, initialTypes []Class) {
	p.codesIsolatedRun[loc.opener] = dirPair
	p.codesIsolatedRun[loc.closer] = dirPair
	for i := loc.opener + 1; i < loc.closer; i++ {
		if initialTypes[p.indexes[i]] != NSM {
			break
		}
		p.codesIsolatedRun[i] = dirPair
	}
	for i := loc.closer + 1; i < len(p.indexes); i++ {
		if initialTypes[p.indexes[i]] != NSM {
			break
		}
		p.codesIsolatedRun[i] = dirPair
	}
}

func (p *bracketPairer) resolveBrackets(dirEmbed Class, initialTypes []Class) {
	for _, loc := range p.pairPositions {
		p.assignBracketType(loc, dirEmbed, initialTypes)
	}
}
