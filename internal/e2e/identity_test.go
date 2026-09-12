package e2e

import (
	"debug/buildinfo"
	"debug/pe"
	"os"
	"runtime"
	"testing"
)

// The environment names what this run is supposed to be. A workflow that means
// to test a native Windows build on Windows sets both, and a run that ends up
// somewhere else then fails here rather than reporting a pass for a platform it
// never touched.
const (
	expectGOOSEnv   = "TWIXTUI_EXPECT_GOOS"
	expectGOARCHEnv = "TWIXTUI_EXPECT_GOARCH"
)

// TestNativeExecutionIdentity checks that this suite is running where it claims
// to be, and against a binary built for the same place.
//
// A cross-compiled artefact is the thing this rules out. "Builds for Windows"
// is a claim a compiler on any machine can make; "runs on Windows" is not, and
// a matrix job whose runner or toolchain drifted would otherwise report the
// second while only ever having established the first. So three separate
// records have to agree: what this test binary was compiled for, what the
// twixtui under test was compiled for — read out of the binary itself rather
// than believed — and, on Windows, what the machine underneath actually is.
func TestNativeExecutionIdentity(t *testing.T) {
	t.Parallel()
	wantOS, wantArch := os.Getenv(expectGOOSEnv), os.Getenv(expectGOARCHEnv)
	if wantOS != "" && wantOS != runtime.GOOS {
		t.Errorf("%s says %s, but these tests are running on %s", expectGOOSEnv, wantOS, runtime.GOOS)
	}
	if wantArch != "" && wantArch != runtime.GOARCH {
		t.Errorf("%s says %s, but these tests are running on %s", expectGOARCHEnv, wantArch, runtime.GOARCH)
	}

	// The binary under test may be one this suite built, or a release artefact
	// named by TWIXTUI_E2E_BINARY. The second is the case worth checking: an
	// artefact downloaded from the wrong matrix leg is a file that runs
	// nowhere, or worse, one that runs under emulation and passes.
	bin := binary(t)
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		t.Fatalf("reading the build information out of %s: %v", bin, err)
	}
	built := map[string]string{}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS", "GOARCH":
			built[setting.Key] = setting.Value
		}
	}
	if built["GOOS"] == "" || built["GOARCH"] == "" {
		t.Fatalf("%s carries no GOOS/GOARCH build settings, so what it was built for cannot be established", bin)
	}
	if built["GOOS"] != runtime.GOOS || built["GOARCH"] != runtime.GOARCH {
		t.Fatalf("%s was built for %s/%s, but these tests run on %s/%s: the suite is driving a binary for another platform",
			bin, built["GOOS"], built["GOARCH"], runtime.GOOS, runtime.GOARCH)
	}

	if runtime.GOOS == "windows" {
		assertPEMachine(t, bin, runtime.GOARCH)
		assertNativeMachine(t, wantArch)
	}
}

// peMachines maps the architectures this project is released for to the value
// a Windows executable carries in its own header.
var peMachines = map[string]uint16{
	"amd64": pe.IMAGE_FILE_MACHINE_AMD64,
	"arm64": pe.IMAGE_FILE_MACHINE_ARM64,
	"386":   pe.IMAGE_FILE_MACHINE_I386,
}

// assertPEMachine reads the architecture out of the executable's own header,
// which is the one record no build setting can contradict.
func assertPEMachine(t *testing.T, path, arch string) {
	t.Helper()
	want, known := peMachines[arch]
	if !known {
		t.Logf("no Windows machine value is recorded for %s, so the executable header was not checked", arch)
		return
	}
	file, err := pe.Open(path)
	if err != nil {
		t.Fatalf("reading the executable header of %s: %v", path, err)
	}
	defer file.Close()
	if file.Machine != want {
		t.Errorf("%s is a Windows executable for machine %#x, want %#x for %s", path, file.Machine, want, arch)
	}
}

// assertNativeMachine checks the processor underneath is the one being tested
// rather than one emulating it. Windows on arm64 runs amd64 binaries, and it
// runs them well enough that a whole suite can pass without a single
// instruction of the architecture the job is named after being executed.
//
// Without an expectation set this is reported rather than failed: running the
// suite under emulation on purpose is a reasonable thing to do, and only a
// caller that said which architecture it wanted is in a position to call it
// wrong.
func assertNativeMachine(t *testing.T, wantArch string) {
	t.Helper()
	process, native, err := nativeMachineIdentity()
	if err != nil {
		if wantArch != "" {
			t.Errorf("%s says %s, but this machine's own architecture could not be read to confirm it: %v",
				expectGOARCHEnv, wantArch, err)
		} else {
			t.Logf("this machine's own architecture could not be read: %v", err)
		}
		return
	}
	arch := machineArch(native)
	if arch == "" {
		if wantArch != "" {
			t.Fatalf("native architecture is unrecognized (%#x); cannot prove native %s execution", native, wantArch)
		}
		t.Logf("this machine reports the unrecognised machine value %#x", native)
		return
	}
	if arch == runtime.GOARCH {
		return
	}
	if wantArch != "" {
		t.Errorf("this suite is built for %s and the machine is %s, so it is running emulated and proves nothing about native %s (the process reports machine %#x)",
			runtime.GOARCH, arch, wantArch, process)
	} else {
		t.Logf("running %s under emulation on a %s machine", runtime.GOARCH, arch)
	}
}

// machineArch names the Go architecture a machine value stands for.
func machineArch(machine uint16) string {
	for arch, value := range peMachines {
		if value == machine {
			return arch
		}
	}
	return ""
}
