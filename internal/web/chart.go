package web

import (
	"math"
	"strconv"
	"time"

	"github.com/back-to-code/sante/internal/store"
)

const chartBuckets = 144

type tick struct {
	Pos   float64
	Label string
}

// Height is the average latency in percent of the plot height; zero unless Up.
type window struct {
	Height float64
	Up     bool
	Failed bool
}

type chartBucket struct {
	Label    string  `json:"t"`
	Avg      float64 `json:"v"`
	Checks   int     `json:"n"`
	Failures int     `json:"f"`
}

type chart struct {
	Empty   bool
	Windows []window
	YTicks  []tick
	XTicks  []tick
	Buckets []chartBucket
	Average string
}

func buildChart(results []store.Result, from, to time.Time, loc *time.Location) chart {
	span := to.Sub(from)
	width := span / chartBuckets
	sums := make([]float64, chartBuckets)
	ups := make([]int, chartBuckets)
	buckets := make([]chartBucket, chartBuckets)
	for i := range buckets {
		start := from.Add(time.Duration(i) * width).In(loc)
		buckets[i] = chartBucket{Label: start.Format("15:04") + "–" + start.Add(width).Format("15:04"), Avg: -1}
	}

	var total float64
	var totalUp int
	for _, r := range results {
		i := int(r.CheckedAt.Sub(from) / width)
		if i < 0 || i >= chartBuckets {
			continue
		}
		buckets[i].Checks++
		if !r.Up {
			buckets[i].Failures++
			continue
		}
		ms := float64(r.Latency) / float64(time.Millisecond)
		sums[i] += ms
		ups[i]++
		total += ms
		totalUp++
	}

	c := chart{Empty: len(results) == 0, Buckets: buckets, Average: "—"}
	if totalUp > 0 {
		c.Average = formatLatency(time.Duration(total / float64(totalUp) * float64(time.Millisecond)))
	}

	var peak float64
	for i := range buckets {
		if ups[i] > 0 {
			buckets[i].Avg = math.Round(sums[i]/float64(ups[i])*10) / 10
			peak = max(peak, buckets[i].Avg)
		}
	}

	step, top := niceScale(peak)
	for v := 0.0; v <= top+step/2; v += step {
		c.YTicks = append(c.YTicks, tick{Pos: (1 - v/top) * 100, Label: axisMillis(v, top)})
	}
	for t := from.In(loc).Truncate(time.Hour).Add(time.Hour); t.Before(to); t = t.Add(time.Hour) {
		// Past 92% the label would run into "Now" at the right edge.
		if pos := float64(t.Sub(from)) / float64(span) * 100; t.Hour()%6 == 0 && pos < 92 {
			c.XTicks = append(c.XTicks, tick{Pos: pos, Label: t.Format("15:04")})
		}
	}

	c.Windows = make([]window, chartBuckets)
	for i, b := range buckets {
		c.Windows[i] = window{Up: ups[i] > 0, Failed: b.Failures > 0}
		if ups[i] > 0 {
			c.Windows[i].Height = math.Round(b.Avg/top*1000) / 10
		}
	}
	return c
}

// niceScale picks a 1/2/2.5/5×10ⁿ tick step giving about four intervals up to peak.
func niceScale(peak float64) (step, top float64) {
	if peak <= 0 {
		return 25, 100
	}
	raw := peak / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	switch norm := raw / mag; {
	case norm <= 1:
		step = mag
	case norm <= 2:
		step = 2 * mag
	case norm <= 2.5:
		step = 2.5 * mag
	case norm <= 5:
		step = 5 * mag
	default:
		step = 10 * mag
	}
	return step, math.Ceil(peak/step) * step
}

func axisMillis(v, top float64) string {
	if top >= 2000 {
		return strconv.FormatFloat(v/1000, 'f', -1, 64) + " s"
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + " ms"
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
