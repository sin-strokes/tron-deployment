package render

import (
	"strings"
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

func TestParseMemoryGB(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"16GB", 16},
		{"32gb", 32},
		{"8G", 8},
		{"64g", 64},
		{"4096MB", 4},
		{"2048M", 2},
		{"", 0},
		{"garbage", 0},
		{"-4GB", 0},
		{" 16 GB ", 16},
	}
	for _, c := range cases {
		if got := ParseMemoryGB(c.in); got != c.want {
			t.Errorf("ParseMemoryGB(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCalculateHeapMax(t *testing.T) {
	cases := []struct {
		memGB int
		want  string
	}{
		{64, "24g"},
		{96, "24g"}, // 64GB+ tier
		{32, "14g"},
		{31, "8g"}, // falls into 16GB+ tier
		{16, "8g"},
		{8, "4g"},
		{4, "2g"},
	}
	for _, c := range cases {
		got := calculateHeapMax(c.memGB, nil)
		if got != c.want {
			t.Errorf("calculateHeapMax(%dGB) = %s, want %s", c.memGB, got, c.want)
		}
	}
}

func TestCalculateHeapMax_Override(t *testing.T) {
	jvm := &intent.JVMConfig{HeapMax: "12g"}
	if got := calculateHeapMax(32, jvm); got != "12g" {
		t.Errorf("override ignored: got %s", got)
	}
}

func TestSelectGC(t *testing.T) {
	cases := []struct {
		jdk  int
		want string
	}{
		{8, "CMS"},
		{11, "CMS"},
		{17, "G1"},
		{21, "G1"},
	}
	for _, c := range cases {
		if got := selectGC(c.jdk, nil); got != c.want {
			t.Errorf("selectGC(jdk=%d) = %s, want %s", c.jdk, got, c.want)
		}
	}
}

func TestSelectGC_Override(t *testing.T) {
	jvm := &intent.JVMConfig{GC: "G1"}
	if got := selectGC(8, jvm); got != "G1" {
		t.Errorf("override ignored: got %s", got)
	}
	jvm.GC = "auto"
	if got := selectGC(8, jvm); got != "CMS" {
		t.Errorf("auto should delegate to default: got %s", got)
	}
}

func TestJVMArgs_DefaultIsHeapOnly(t *testing.T) {
	// With nil JVM config, GC selection and GC logging stay off so the args
	// don't collide with the java-tron image's own start.sh tuning.
	args := JVMArgs(32, 17, nil)
	joined := strings.Join(args, " ")

	for _, want := range []string{"-Xmx14g", "-Xms14g", "-XX:+HeapDumpOnOutOfMemoryError"} {
		if !strings.Contains(joined, want) {
			t.Errorf("JVMArgs missing %q in: %s", want, joined)
		}
	}
	for _, unwanted := range []string{"-XX:+UseG1GC", "-XX:+UseConcMarkSweepGC", "-Xlog:gc", "gc.log"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("JVMArgs should not include %q by default: %s", unwanted, joined)
		}
	}
}

func TestJVMArgs_GCOptIn(t *testing.T) {
	args := JVMArgs(32, 17, &intent.JVMConfig{GC: "G1"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-XX:+UseG1GC") {
		t.Errorf("opt-in G1 not emitted: %s", joined)
	}
}

func TestJVMArgs_GCAutoStaysOff(t *testing.T) {
	args := JVMArgs(32, 17, &intent.JVMConfig{GC: "auto"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-XX:+UseG1GC") || strings.Contains(joined, "-XX:+UseConcMarkSweepGC") {
		t.Errorf("auto should stay off: %s", joined)
	}
}

func TestJVMArgs_GCLogOptIn(t *testing.T) {
	on := intent.BoolPtr(true)
	args := JVMArgs(16, 17, &intent.JVMConfig{GCLog: on})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-Xlog:gc") {
		t.Errorf("GC log should be enabled when opted in: %s", joined)
	}
}

func TestJVMArgsString_SpaceJoined(t *testing.T) {
	s := JVMArgsString(16, 17, nil)
	if !strings.HasPrefix(s, "-Xmx") {
		t.Errorf("unexpected prefix: %s", s)
	}
	if strings.Contains(s, "  ") {
		t.Errorf("double space in args: %s", s)
	}
}
