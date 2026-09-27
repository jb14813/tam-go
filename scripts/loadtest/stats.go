package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// phase is one part of the event. Every request made while it runs is
// recorded in it, including the status bars' and the admin page's.
type phase struct {
	name       string
	start, end time.Time
	note       string // one line on what happened, for the report
	rec        recorder
}

func (ph *phase) duration() time.Duration {
	if ph.end.IsZero() {
		return time.Since(ph.start)
	}
	return ph.end.Sub(ph.start)
}

// recorder collects the timings of the requests of one phase, per action.
type recorder struct {
	mu    sync.Mutex
	ops   map[string]*opStats
	order []string
}

type opStats struct {
	times  []time.Duration
	errors int
	rows   int
	queued int
	first  []string // the first few errors
}

func (r *recorder) add(op string, took time.Duration, err error, rows int, queued bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ops == nil {
		r.ops = map[string]*opStats{}
	}
	s := r.ops[op]
	if s == nil {
		s = &opStats{}
		r.ops[op] = s
		r.order = append(r.order, op)
	}
	s.times = append(s.times, took)
	s.rows += rows
	if queued {
		s.queued++
	}
	if err != nil {
		s.errors++
		if len(s.first) < 3 {
			s.first = append(s.first, err.Error())
		}
	}
}

// totals returns the requests, errors, rows written and saves queued of the
// phase, and the first few errors.
func (r *recorder) totals() (requests, errors, rows, queued int, first []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, op := range r.order {
		s := r.ops[op]
		requests += len(s.times)
		errors += s.errors
		rows += s.rows
		queued += s.queued
		first = append(first, s.first...)
	}
	return requests, errors, rows, queued, first
}

// saves returns how many requests of the phase wrote rows.
func (r *recorder) saves() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.ops {
		if s.rows > 0 {
			n += len(s.times)
		}
	}
	return n
}

// table writes the timing of every action of the phase.
func (r *recorder) table(b *strings.Builder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(b, "    %-30s %7s %7s %8s %8s %8s %8s %6s %6s\n", "action", "count", "rows", "median", "p95", "p99", "max", "errors", "queued")
	for _, op := range r.order {
		s := r.ops[op]
		sorted := slices.Clone(s.times)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		rows := ""
		if s.rows > 0 {
			rows = fmt.Sprint(s.rows)
		}
		fmt.Fprintf(b, "    %-30s %7d %7s %8s %8s %8s %8s %6d %6d\n", op, len(sorted), rows,
			ms(percentile(sorted, 0.50)), ms(percentile(sorted, 0.95)), ms(percentile(sorted, 0.99)), ms(percentile(sorted, 1)),
			s.errors, s.queued)
	}
}

// percentile returns the q-th quantile of sorted durations.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q*float64(len(sorted))+0.5) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// ms writes a duration in milliseconds, with a decimal below ten.
func ms(d time.Duration) string {
	m := float64(d) / float64(time.Millisecond)
	switch {
	case m < 10:
		return fmt.Sprintf("%.1fms", m)
	case m < 10000:
		return fmt.Sprintf("%.0fms", m)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// secs writes a duration in seconds with one decimal.
func secs(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

// megabytes writes a size in MB.
func megabytes(n uint64) string { return fmt.Sprintf("%.0f MB", float64(n)/(1<<20)) }

// problems collects what went wrong along the way, by kind, with the first
// few descriptions of each.
type problems struct {
	mu    sync.Mutex
	count map[string]int
	first map[string][]string
}

func (p *problems) add(kind string, n int, format string, args ...any) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count == nil {
		p.count, p.first = map[string]int{}, map[string][]string{}
	}
	p.count[kind] += n
	if len(p.first[kind]) < 3 {
		p.first[kind] = append(p.first[kind], fmt.Sprintf(format, args...))
	}
}

func (p *problems) get(kind string) (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count[kind], p.first[kind]
}
