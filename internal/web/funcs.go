package web

import (
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
)

// Approximations of the Material 3 Expressive shape library as polar curves,
// drawn in a 0 0 100 100 viewBox. SHAPES in app.js must match: it morphs
// between them starting from the paths drawn here.
var shapeSpecs = map[string]struct {
	kind string
	n, a float64
}{
	"circle":    {"smooth", 0, 0},
	"cookie9":   {"scallop", 9, 0.07},
	"cookie12":  {"scallop", 12, 0.05},
	"sunny":     {"smooth", 8, 0.05},
	"burst":     {"burst", 12, 0.11},
	"softburst": {"smooth", 10, 0.08},
	"clover4":   {"scallop", 4, 0.2},
	"flower":    {"scallop", 8, 0.12},
	"puffy":     {"smooth", 6, 0.07},
	"square":    {"squircle", 4, 0},
}

var shapes = func() map[string]string {
	out := make(map[string]string, len(shapeSpecs))
	for name, s := range shapeSpecs {
		const points = 180
		radii := make([]float64, points)
		var peak float64
		for i := range radii {
			t := 2 * math.Pi * float64(i) / points
			r := 1.0
			switch s.kind {
			case "smooth":
				r = 1 + s.a*math.Cos(s.n*t)
			case "scallop":
				r = 1 + s.a*(2*math.Abs(math.Cos(s.n*t/2))-1)
			case "burst":
				r = 1 + s.a*(1-2*math.Abs(math.Sin(s.n*t/2)))
			case "squircle":
				r = math.Pow(math.Pow(math.Abs(math.Cos(t)), s.n)+math.Pow(math.Abs(math.Sin(t)), s.n), -1/s.n)
			}
			radii[i] = r
			peak = max(peak, r)
		}
		var b strings.Builder
		for i, r := range radii {
			t := 2 * math.Pi * float64(i) / points
			r *= 48 / peak
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			b.WriteString(cmd + fixed2(50+r*math.Cos(t)) + " " + fixed2(50+r*math.Sin(t)))
		}
		out[name] = b.String() + "Z"
	}
	return out
}()

func fixed2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// Motion is "period spin turn" for app.js: milliseconds per shape, degrees
// per second and degrees per morph. Empty keeps the shape still.
type shapeView struct {
	Class  string
	Shapes string
	Path   string
	Icon   string
	Motion string
}

func mshape(class, names, icon, motion string) shapeView {
	first, _, _ := strings.Cut(names, " ")
	return shapeView{Class: class, Shapes: names, Path: shapes[first], Icon: icon, Motion: motion}
}

// Material Symbols Rounded paths, inlined so icons render without the icon
// font. The -bold variants are weight 700.
var icons = map[string]string{
	"check":      `m382-354 339-339q12-12 28-12t28 12q12 12 12 28.5T777-636L410-268q-12 12-28 12t-28-12L182-440q-12-12-11.5-28.5T183-497q12-12 28.5-12t28.5 12l142 143Z`,
	"check-bold": `m382-388 321-321q19-19 45-19t45 19q19 19 19 45t-19 45L427-253q-19 19-45 19t-45-19L167-423q-19-19-19-45t19-45q19-19 45-19t45 19l125 125Z`,
	"close":      `M480-424 284-228q-11 11-28 11t-28-11q-11-11-11-28t11-28l196-196-196-196q-11-11-11-28t11-28q11-11 28-11t28 11l196 196 196-196q11-11 28-11t28 11q11 11 11 28t-11 28L536-480l196 196q11 11 11 28t-11 28q-11 11-28 11t-28-11L480-424Z`,
	"close-bold": `M480-392 300-212q-18 18-44 18t-44-18q-18-18-18-44t18-44l180-180-180-180q-18-18-18-44t18-44q18-18 44-18t44 18l180 180 180-180q18-18 44-18t44 18q18 18 18 44t-18 44L568-480l180 180q18 18 18 44t-18 44q-18 18-44 18t-44-18L480-392Z`,
	"priority":   `M479.85-66Q434-66 401.5-98.65 369-131.3 369-177.15t32.65-78.35q32.65-32.5 78.5-32.5t78.35 32.65q32.5 32.65 32.5 78.5T558.35-98.5Q525.7-66 479.85-66Zm.65-302q-39.5 0-67.5-27.91-28-27.9-28-67.09v-328q0-39.19 27.5-67.09Q440-886 479.5-886t67.5 27.91q28 27.9 28 67.09v328q0 39.19-27.5 67.09Q520-368 480.5-368Z`,
	"monitor":    `M172-126q-53 0-89.5-36.5T46-252v-125q0-26 18.5-44.5T109-440h186l69 138q5 11 15 16.5t21 5.5q11 0 21-5.5t15-16.5l124-248 44 88q5 11 15 16.5t21 5.5h211q26 0 44.5 18.5T914-377v125q0 53-36.5 89.5T788-126H172ZM46-708q0-53 36.5-89.5T172-834h616q53 0 89.5 36.5T914-708v125q0 26-18.5 44.5T851-520H665l-69-138q-5-11-15-15.5t-21-4.5q-11 0-21 4.5T524-658L400-410l-44-88q-5-11-15-16.5t-21-5.5H109q-26 0-44.5-18.5T46-583v-125Z`,
	"database":   `M759-575q115-52 115-124 0-73-115-124t-280-51q-165 0-279 51T86-699q0 72 114 124t279 52q165 0 280-52ZM622.5-447.5Q693-460 749-483t91-55.5q35-32.5 35-72.5v130q0 40-35 72.5T749-353q-56 23-126.5 35.5T480-305q-72 0-142.5-12.5T211-353q-56-23-91-55.5T85-481v-130q0 40 35 72.5t91 55.5q56 23 126.5 35.5T480-435q72 0 142.5-12.5Zm0 218Q693-242 749-265.5t91-56q35-32.5 35-72.5v131q0 40-35 72.5T749-135q-56 23-126.5 35.5T480-87q-72 0-142.5-12.5T211-135q-56-23-91-55.5T85-263v-131q0 40 35 72.5t91 55.5q56 23 126.5 36T480-217q72 0 142.5-12.5Z`,
	"schedule":   `M535-504v-116q0-23.38-16-39.19Q503-675 480-675t-39 15.81q-16 15.81-16 39.19v136q0 13 4.5 24.07T443-440l116 116q16 16 38 16t39-16q17-16 17-39t-17-40L535-504ZM480-46q-91 0-169.99-34.08-78.98-34.09-137.41-92.52-58.43-58.43-92.52-137.41Q46-389 46-480q0-91 34.08-169.99 34.09-78.98 92.52-137.41 58.43-58.43 137.41-92.52Q389-914 480-914q91 0 169.99 34.08 78.98 34.09 137.41 92.52 58.43 58.43 92.52 137.41Q914-571 914-480q0 91-34.08 169.99-34.09 78.98-92.52 137.41-58.43 58.43-137.41 92.52Q571-46 480-46Z`,
	"auto":       `M324-111.5Q251-143 197-197t-85.5-127Q80-397 80-480t31.5-156Q143-709 197-763t127-85.5Q397-880 480-880t156 31.5Q709-817 763-763t85.5 127Q880-563 880-480t-31.5 156Q817-251 763-197t-127 85.5Q563-80 480-80t-156-31.5ZM520-163q119-15 199.5-104.5T800-480q0-123-80.5-212.5T520-797v634Z`,
	"dark":       `M480-120q-151 0-255.5-104.5T120-480q0-138 90-239.5T440-838q13-2 23 3.5t16 14.5q6 9 6.5 21t-7.5 23q-17 26-25.5 55t-8.5 61q0 90 63 153t153 63q31 0 61.5-9t54.5-25q11-7 22.5-6.5T819-479q10 5 15.5 15t3.5 24q-14 138-117.5 229T480-120Z`,
	"light":      `M338.5-338.5Q280-397 280-480t58.5-141.5Q397-680 480-680t141.5 58.5Q680-563 680-480t-58.5 141.5Q563-280 480-280t-141.5-58.5ZM80-440q-17 0-28.5-11.5T40-480q0-17 11.5-28.5T80-520h80q17 0 28.5 11.5T200-480q0 17-11.5 28.5T160-440H80Zm720 0q-17 0-28.5-11.5T760-480q0-17 11.5-28.5T800-520h80q17 0 28.5 11.5T920-480q0 17-11.5 28.5T880-440h-80ZM451.5-771.5Q440-783 440-800v-80q0-17 11.5-28.5T480-920q17 0 28.5 11.5T520-880v80q0 17-11.5 28.5T480-760q-17 0-28.5-11.5Zm0 720Q440-63 440-80v-80q0-17 11.5-28.5T480-200q17 0 28.5 11.5T520-160v80q0 17-11.5 28.5T480-40q-17 0-28.5-11.5ZM226-678l-43-42q-12-11-11.5-28t11.5-29q12-12 29-12t28 12l42 43q11 12 11 28t-11 28q-11 12-27.5 11.5T226-678Zm494 495-42-43q-11-12-11-28.5t11-27.5q11-12 27.5-11.5T734-282l43 42q12 11 11.5 28T777-183q-12 12-29 12t-28-12Zm-42-495q-12-11-11.5-27.5T678-734l42-43q11-12 28-11.5t29 11.5q12 12 12 29t-12 28l-43 42q-12 11-28 11t-28-11ZM183-183q-12-12-12-29t12-28l43-42q12-11 28.5-11t27.5 11q12 11 11.5 27.5T282-226l-42 43q-11 12-28 11.5T183-183Z`,
	"forward":    `M647-440H200q-17 0-28.5-11.5T160-480q0-17 11.5-28.5T200-520h447L451-716q-12-12-11.5-28t12.5-28q12-11 28-11.5t28 11.5l264 264q6 6 8.5 13t2.5 15q0 8-2.5 15t-8.5 13L508-188q-11 11-27.5 11T452-188q-12-12-12-28.5t12-28.5l195-195Z`,
	"chevron":    `M504-480 348-636q-11-11-11-28t11-28q11-11 28-11t28 11l184 184q6 6 8.5 13t2.5 15q0 8-2.5 15t-8.5 13L404-268q-11 11-28 11t-28-11q-11-11-11-28t11-28l156-156Z`,
	"back":       `m313-440 196 196q12 12 11.5 28T508-188q-12 11-28 11.5T452-188L188-452q-6-6-8.5-13t-2.5-15q0-8 2.5-15t8.5-13l264-264q11-11 27.5-11t28.5 11q12 12 12 28.5T508-715L313-520h447q17 0 28.5 11.5T800-480q0 17-11.5 28.5T760-440H313Z`,
	"code":       `m193-479 155 155q11 11 11 28t-11 28q-11 11-28 11t-28-11L108-452q-6-6-8.5-13T97-480q0-8 2.5-15t8.5-13l184-184q12-12 28.5-12t28.5 12q12 12 12 28.5T349-635L193-479Zm574-2L612-636q-11-11-11-28t11-28q11-11 28-11t28 11l184 184q6 6 8.5 13t2.5 15q0 8-2.5 15t-8.5 13L668-268q-12 12-28 11.5T612-269q-12-12-12-28.5t12-28.5l155-155Z`,
	"newest":     `M440-647 244-451q-12 12-28 11.5T188-452q-11-12-11.5-28t11.5-28l264-264q6-6 13-8.5t15-2.5q8 0 15 2.5t13 8.5l264 264q11 11 11 27.5T772-452q-12 12-28.5 12T715-452L520-647v447q0 17-11.5 28.5T480-160q-17 0-28.5-11.5T440-200v-447Z`,
	"older":      `M440-313v-447q0-17 11.5-28.5T480-800q17 0 28.5 11.5T520-760v447l196-196q12-12 28-11.5t28 12.5q11 12 11.5 28T772-452L508-188q-6 6-13 8.5t-15 2.5q-8 0-15-2.5t-13-8.5L188-452q-11-11-11-27.5t11-28.5q12-12 28.5-12t28.5 12l195 195Z`,
}

func icon(name string) template.HTML {
	return template.HTML(`<svg class="icon" viewBox="0 -960 960 960" aria-hidden="true"><path d="` + icons[name] + `"/></svg>`)
}

func templateFuncs(loc *time.Location) template.FuncMap {
	return template.FuncMap{
		"icon":        icon,
		"mshape":      mshape,
		"themeScript": func() template.JS { return themeScript },
		"iso": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.UTC().Format(time.RFC3339)
		},
		"ago":   func(t time.Time) string { return ago(time.Since(t)) },
		"clock": func(t time.Time) string { return t.In(loc).Format("Jan 2, 15:04:05") },
	}
}

// ago mirrors the relative-time formatting in app.js, which takes over after load.
func ago(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return formatCount(int(d.Seconds())) + "s ago"
	case d < time.Hour:
		return formatCount(int(d.Minutes())) + "m ago"
	case d < 48*time.Hour:
		return formatCount(int(d.Hours())) + "h ago"
	}
	return formatCount(int(d.Hours()/24)) + "d ago"
}
