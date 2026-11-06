package platform

import (
	"net/netip"
	"os"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	p, err := readProcStat(os.Getpid())
	if err != nil || p.pid != os.Getpid() || p.ppid != os.Getppid() || p.start == 0 || p.zombie {
		t.Fatalf("own stat: %+v, %v", p, err)
	}
	line := "42 (a) (b c) Z 7 9 9 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0\n"
	p, err = parseProcStat([]byte(line))
	if err != nil || p != (procInfo{pid: 42, ppid: 7, pgid: 9, start: 12345, zombie: true}) {
		t.Fatalf("parse %q: %+v, %v", line, p, err)
	}
	for _, bad := range []string{"", "42 no name", "42 (x) S 1 2"} {
		if _, err := parseProcStat([]byte(bad)); err == nil {
			t.Errorf("parse %q should fail", bad)
		}
	}
}

func TestParseProcNetAddr(t *testing.T) {
	cases := map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"0000000000000000FFFF00000100007F:0050": "127.0.0.1:80",
		"00000000000000000000000001000000:01BB": "[::1]:443",
	}
	for in, want := range cases {
		got, err := parseProcNetAddr(in)
		if err != nil || got != netip.MustParseAddrPort(want) {
			t.Errorf("parse %s: %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0100007F", "XYZ:1", "0100:1", "0100007F:FFFFF"} {
		if _, err := parseProcNetAddr(bad); err == nil {
			t.Errorf("parse %q should fail", bad)
		}
	}
}
