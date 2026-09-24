package codehealth

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
)

// dupWindow is the minimum number of consecutive significant lines that
// must match for a block to count as duplicated.
const dupWindow = 8

// Clone is one duplicated block and every place it appears.
type Clone struct {
	Lines     int        `json:"lines"`
	Instances []Instance `json:"instances"`
	Sample    string     `json:"sample,omitempty"`
}

// Instance is one occurrence of a clone.
type Instance struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type sigLine struct {
	text string
	line int // 1-based original line
}

var trivialLine = regexp.MustCompile(`^[\s{}()\[\];,]*$|^(import|from|package|using|use|require|include|#include|@)\b`)

// significant returns normalised lines worth comparing: comments/strings
// are blanked by stripCode, whitespace collapsed, and braces-only or
// import lines dropped.
func significant(src []byte, lang string) []sigLine {
	lines := stripCode(string(src), lang == "Python" || lang == "Ruby" || lang == "Shell" || lang == "PHP")
	var out []sigLine
	for i, l := range lines {
		t := strings.Join(strings.Fields(l), " ")
		if len(t) < 4 || trivialLine.MatchString(t) {
			continue
		}
		out = append(out, sigLine{t, i + 1})
	}
	return out
}

type fileLines struct {
	path  string
	lines []sigLine
}

// findClones is a CPD-style detector: hash every window of dupWindow
// significant lines, group identical windows, then extend matches into
// maximal blocks.
func findClones(files []fileLines) []Clone {
	type loc struct{ f, i int }
	index := map[uint64][]loc{}
	for fi, f := range files {
		for i := 0; i+dupWindow <= len(f.lines); i++ {
			h := fnv.New64a()
			for _, l := range f.lines[i : i+dupWindow] {
				h.Write([]byte(l.text))
				h.Write([]byte{'\n'})
			}
			index[h.Sum64()] = append(index[h.Sum64()], loc{fi, i})
		}
	}
	covered := map[loc]bool{}
	var clones []Clone
	keys := make([]uint64, 0, len(index))
	for k, v := range index {
		if len(v) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := index[keys[i]][0], index[keys[j]][0]
		if a.f != b.f {
			return a.f < b.f
		}
		return a.i < b.i
	})
	for _, k := range keys {
		locs := index[k]
		if covered[locs[0]] {
			continue
		}
		// Verify text equality (hash collisions) and drop overlapping self-matches.
		first := locs[0]
		var same []loc
		for _, l := range locs {
			if equalWindow(files[first.f].lines[first.i:first.i+dupWindow], files[l.f].lines[l.i:l.i+dupWindow]) {
				if len(same) > 0 {
					prev := same[len(same)-1]
					if prev.f == l.f && l.i < prev.i+dupWindow {
						continue
					}
				}
				same = append(same, l)
			}
		}
		if len(same) < 2 {
			continue
		}
		// Extend while every instance keeps matching.
		n := dupWindow
		for {
			ok := true
			for _, l := range same {
				if l.i+n >= len(files[l.f].lines) || files[l.f].lines[l.i+n].text != files[first.f].lines[first.i+n].text {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			n++
		}
		c := Clone{}
		for _, l := range same {
			ls := files[l.f].lines
			c.Instances = append(c.Instances, Instance{Path: files[l.f].path, StartLine: ls[l.i].line, EndLine: ls[l.i+n-1].line})
			for j := 0; j < n; j++ {
				covered[loc{l.f, l.i + j}] = true
			}
		}
		c.Lines = c.Instances[0].EndLine - c.Instances[0].StartLine + 1
		var sample []string
		for _, l := range files[first.f].lines[first.i:min(first.i+3, first.i+n)] {
			sample = append(sample, l.text)
		}
		c.Sample = strings.Join(sample, "\n")
		clones = append(clones, c)
	}
	sort.Slice(clones, func(i, j int) bool {
		wi, wj := clones[i].Lines*len(clones[i].Instances), clones[j].Lines*len(clones[j].Instances)
		if wi != wj {
			return wi > wj
		}
		return clones[i].Instances[0].Path < clones[j].Instances[0].Path
	})
	return clones
}

func equalWindow(a, b []sigLine) bool {
	for i := range a {
		if a[i].text != b[i].text {
			return false
		}
	}
	return true
}
