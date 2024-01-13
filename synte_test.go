package synte

import "testing"

func init() {
	msg = func(s string, i ...interface{}) {
		// eliding info message
	}
}

var results = [4]string{
	"startNewOperation",
	"startNewListing",
	"exitNow",
	"nextOperation",
}

var testChecks = []struct {
	check func(*systemState) int
	name string
	i systemState // input
	op string     // operator
	opd string    // operand
	num bool
	o int		  // expected return value
}{
	{check: noCheck, name: "noCheck", o: nextOperation},
	{check: checkOut, name: "checkOut", op: "out", opd: "vca", o: nextOperation},
	{check: checkOut, name: "checkOut", op: "out", opd: "3hz", o: startNewOperation, num: true},
	{check: checkOut, name: "checkOut", op: "out+", opd: "extant", o: nextOperation},
	{check: checkOut, name: "checkOut", op: "out", opd: "^freq", o: nextOperation},
	{check: checkOut, name: "checkOut", op: "out", opd: "extant", o: startNewOperation},
	{check: checkOut, name: "checkOut", op: "out", opd: "@", o: startNewOperation},
	{check: checkIndexIncl, name: "checkIndexIncl", op: "level", opd: "0", o: nextOperation, num: true},
	{check: checkIndexIncl, name: "checkIndexIncl", op: "level", opd: "Z", o: startNewOperation},
	{check: checkIndexIncl, name: "checkIndexIncl", op: "level", opd: "2", o: startNewOperation},
	{check: checkIndex, name: "checkIndex", op: "from", opd: "0", o: nextOperation, num: true},
	{check: checkIndex, name: "checkIndex", op: "from", opd: "Z", o: startNewOperation},
	{check: checkIndex, name: "checkIndex", op: "from", opd: "1", o: startNewOperation},
	{check: checkFade, name: "checkFade", op: "fade", opd: "Z", o: startNewOperation, num: false},
	{check: checkFade, name: "checkFade", op: "fade", opd: "125ms", o: startNewOperation, num: true},
	{check: checkRelease, name: "checkRelease", op: "release", opd: "125ms", o: startNewOperation, num: true},
	{check: checkRelease, name: "checkRelease", op: "release", opd: "Z", o: startNewOperation, num: false},
}

func TestChecks(t *testing.T) {
	for i, tst := range testChecks {
		switch tst.name  { // initialising here because embedded struct literals are awkward
		case "checkOut":
			tst.i.out = map[string]struct{}{
				"extant": struct{}{},
				"^freq": struct{}{},
			}
			tst.i.clr = func(s string, i ...interface{}) int {
				// eliding info message
				return startNewOperation
			}
		case "checkIndexIncl":
			transfer.Listing = make([]listing, 1)
		}
		tst.i.operator = tst.op
		tst.i.operand = tst.opd
		tst.i.num.Is = tst.num
		res := tst.check(&tst.i)
		if res != tst.o {
			t.Errorf(`#%d %s (%q) => %s, expected %s`, i, tst.name, tst.opd, results[res], results[tst.o])
		}
	}
}

func TestParseType(t *testing.T) {
	if SampleRate != 48e3 {
		t.Fatal(`Change sample rate to 48,000`)
	}
	tests := []struct {
		op, expr string
		n float64
		b bool
	}{
		{"in", "1/2", 0.5, true},
		{"in", "500", 0, false},
		{"in", "500!", 500, true},
		{"in", "1ms", 1e3/SampleRate, true},
		{"in", "2e-2ms", 0, false},
		{"in", "4e3bpm", 0, false},
		{"in", "120bpm", 2/SampleRate, true},
		{"in", "1/48m", 1/6e4, true},
		{"in", "24khz", 0.5, true},
		{"in", "48e3hz", 1, true},
		{"in", "48*2e3hz", 0, false},
		{"in", "0db", 1, true},
		{"in", "1*+-3x", 0, false},
	}
	for _, tst := range tests {
		if n, b := parseType(tst.expr, tst.op); n != tst.n || b != tst.b {
			t.Errorf(`parseType(%q, %q) => %g %v, expected %g %v`, tst.expr, tst.op, n, b, tst.n, tst.b)
		}
	}
}
