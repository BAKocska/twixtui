//go:build windows

// This file is the Windows terminal: a ConPTY pseudoconsole created and owned
// by the test process, with the program's output parsed by a virtual terminal
// emulator so that a frame can be read back. There is no tmux and no shell in
// the picture, and nothing here needs a Unix environment to be installed.
//
// The shape of it comes from the pseudoconsole's own contract
// (https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session),
// and the parts of that contract which are not optional are the parts this
// file is organised around:
//
//   - the two communication channels are synchronous pipes, and each one is
//     serviced on its own goroutine. Servicing both from one place is the
//     documented way to deadlock: one buffer fills while its owner is waiting
//     on the other.
//   - the handles given to CreatePseudoConsole are released as soon as the
//     child exists, so that the pipes can ever report a broken channel.
//   - closing the pseudoconsole can emit one final frame, and it terminates
//     every client attached to it. The output channel therefore has to stay
//     drained across the close, which is why teardown closes the pseudoconsole
//     with the drain goroutine still running and only then takes the pipes
//     apart.
//
// The emulator's input side carries more than keystrokes: it is also where the
// emulator writes its answers to the program's own queries, such as device
// attributes and cursor position. A program that asks the terminal something
// at startup, which is what Bubble Tea does, is waiting for an answer that
// arrives on that channel, so it is forwarded by a goroutine of its own rather
// than as a side effect of a capture.

package e2e

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/sys/windows"
)

// The pseudoconsole API lives in kernel32. It is resolved lazily and by hand
// so that its absence is an error this package can report, rather than the
// panic a missing entry point produces the first time it is called.
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procCreatePseudoConsole    = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole    = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole     = kernel32.NewProc("ClosePseudoConsole")
	procUpdateProcThreadAttr   = kernel32.NewProc("UpdateProcThreadAttribute")
	procInitProcThreadAttrList = kernel32.NewProc("InitializeProcThreadAttributeList")
)

const (
	// closeGrace bounds each step of teardown. Every step has a reason to
	// finish at once, so a step that runs out of time is reported rather than
	// waited on further.
	closeGrace = 5 * time.Second
	// maxCell is the largest terminal dimension a pseudoconsole accepts: the
	// COORD it is given is a pair of signed 16-bit values.
	maxCell = 32767
	// Four MiB of queued input decouples emulator reads from the blocking
	// ConPTY pipe. Overflow is an error, never an unbounded allocation or wait.
	inputQueueChunks = 1024
	inputChunkSize   = 4096
)

// Available reports whether this Windows can host a terminal test.
func Available() error {
	for _, proc := range []*windows.LazyProc{
		procCreatePseudoConsole,
		procResizePseudoConsole,
		procClosePseudoConsole,
		procUpdateProcThreadAttr,
		procInitProcThreadAttrList,
	} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("the pseudoconsole API is missing from kernel32: %w", err)
		}
	}
	return nil
}

// requireAvailable fails the test when there is no pseudoconsole.
//
// This is deliberately not a skip. Every Windows this project supports has
// ConPTY, so its absence does not mean "not applicable here", it means the
// terminal tests are measuring nothing — and a suite that skips itself into
// silence on the platform it was written for is worse than one that fails.
func requireAvailable(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		t.Fatalf("cannot host a terminal test on this Windows: %v", err)
	}
}

// conptyTerminal is one program running in a pseudoconsole owned by this
// process.
//
// The emulator is the only state two goroutines touch: the drain goroutine
// writes the program's output into it while the test goroutine takes
// snapshots, sends keys and resizes it. That is what vt.SafeEmulator
// serialises, and every access here goes through it rather than through the
// unlocked methods it promotes from the emulator it wraps. The one exception
// is closing it, which teardown does only after the drain goroutine has
// stopped.
type conptyTerminal struct {
	t         *testing.T
	emu       *vt.SafeEmulator
	inputPipe io.Closer

	console windows.Handle // the pseudoconsole, HPCON
	input   windows.Handle // write end of the channel into the pseudoconsole
	output  windows.Handle // read end of the channel out of it
	process windows.Handle
	thread  windows.Handle
	job     windows.Handle

	drained    chan struct{} // closed when the output goroutine has stopped
	forwarded  chan struct{} // closed when the input goroutine has stopped
	inputQueue chan []byte
	written    chan struct{}
	inputErr   error

	mu            sync.Mutex
	width, height int
	code          int
	exited        bool

	closed bool
}

func startBackend(t *testing.T, command, env []string, opts Options) backend {
	t.Helper()
	if err := checkSize(opts.Width, opts.Height); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tm := &conptyTerminal{
		t:          t,
		emu:        vt.NewSafeEmulator(opts.Width, opts.Height),
		width:      opts.Width,
		height:     opts.Height,
		drained:    make(chan struct{}),
		forwarded:  make(chan struct{}),
		inputQueue: make(chan []byte, inputQueueChunks),
		written:    make(chan struct{}),
	}
	tm.inputPipe = tm.emu.InputPipe().(io.Closer)

	// The channels the pseudoconsole is driven with. They must be synchronous,
	// which is what CreatePipe gives: the pseudoconsole reads and writes them
	// with ReadFile and WriteFile and cannot use an OVERLAPPED structure.
	var childInput, childOutput windows.Handle
	if err := windows.CreatePipe(&childInput, &tm.input, nil, 0); err != nil {
		t.Fatalf("creating the pseudoconsole input pipe: %v", err)
	}
	if err := windows.CreatePipe(&tm.output, &childOutput, nil, 0); err != nil {
		_ = windows.CloseHandle(childInput)
		_ = windows.CloseHandle(tm.input)
		t.Fatalf("creating the pseudoconsole output pipe: %v", err)
	}

	size := windows.Coord{X: int16(opts.Width), Y: int16(opts.Height)}
	if err := windows.CreatePseudoConsole(size, childInput, childOutput, 0, &tm.console); err != nil {
		_ = windows.CloseHandle(childInput)
		_ = windows.CloseHandle(childOutput)
		_ = windows.CloseHandle(tm.input)
		_ = windows.CloseHandle(tm.output)
		t.Fatalf("creating a %dx%d pseudoconsole: %v", opts.Width, opts.Height, err)
	}

	// Both channels are serviced before the program starts and stay serviced
	// until teardown has broken them.
	go tm.drain()
	go tm.forward()
	go tm.writeQueuedInput()

	err := tm.spawn(command, env, opts.Dir)

	// The pseudoconsole holds its own duplicates of these, and while this
	// process holds them too the pipes can never break, so a read on the
	// output would hang forever instead of ending when the program does. They
	// are released whether or not the program started.
	_ = windows.CloseHandle(childInput)
	_ = windows.CloseHandle(childOutput)

	if err != nil {
		tm.close()
		t.Fatalf("starting %s: %v", command[0], err)
	}
	return tm
}

// spawn starts the program attached to the pseudoconsole.
func (tm *conptyTerminal) spawn(command, env []string, dir string) error {
	executable, err := resolveExecutable(command[0])
	if err != nil {
		return err
	}
	executable16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return fmt.Errorf("executable %q: %w", executable, err)
	}
	// The command line is composed with the Windows quoting rules, so an
	// argument containing spaces, backslashes, quotes or characters outside
	// ASCII survives being parsed back apart by the program.
	commandLine16, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command))
	if err != nil {
		return fmt.Errorf("command line: %w", err)
	}
	environment16, err := environmentBlock(env)
	if err != nil {
		return err
	}
	var dir16 *uint16
	if dir != "" {
		if dir16, err = windows.UTF16PtrFromString(dir); err != nil {
			return fmt.Errorf("working directory %q: %w", dir, err)
		}
	}

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("allocating the process attribute list: %w", err)
	}
	// The list is only read while the process is being created.
	defer attributes.Delete()
	if err := setPseudoConsoleAttribute(attributes.List(), tm.console); err != nil {
		return fmt.Errorf("attaching the pseudoconsole to the child: %w", err)
	}

	startup := new(windows.StartupInfoEx)
	startup.Cb = uint32(unsafe.Sizeof(*startup))
	startup.ProcThreadAttributeList = attributes.List()
	// No STARTF_USESTDHANDLES and no inherited handles: the standard handles
	// of a process created with a pseudoconsole are the console's, which is
	// the whole point, and setting them here would take that away.

	info := new(windows.ProcessInformation)
	// EXTENDED_STARTUPINFO_PRESENT is what makes the attribute list, and so
	// the pseudoconsole, visible to the system. CREATE_UNICODE_ENVIRONMENT
	// says the environment block is UTF-16, which is the only form it is built
	// in here. The program is created suspended so that it is inside the job
	// object before it can run, and therefore before it can start a child that
	// would not be.
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT |
		windows.CREATE_UNICODE_ENVIRONMENT |
		windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(executable16, commandLine16, nil, nil, false,
		flags, environment16, dir16, &startup.StartupInfo, info); err != nil {
		return fmt.Errorf("CreateProcess %s: %w", executable, err)
	}
	tm.process, tm.thread = info.Process, info.Thread

	// A job object whose last handle closing kills its members is what keeps a
	// program that starts children from leaving them behind. Without it a
	// child that outlived the program would hold the pseudoconsole open and
	// the next test would inherit the mess, so failing to set it up is a
	// failure to start.
	job, err := createKillOnCloseJob()
	if err != nil {
		return fmt.Errorf("creating the job object for the child: %w", err)
	}
	tm.job = job
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		return fmt.Errorf("assigning the child to its job object: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		return fmt.Errorf("resuming the child: %w", err)
	}
	return nil
}

// resolveExecutable turns command[0] into a path CreateProcess will accept.
//
// CreateProcess resolves a bare or relative application name against the
// current directory of the calling process and does not apply PATHEXT, so the
// lookup is done here, where "the program the caller meant" is still
// well defined: the child may be given a different working directory.
func resolveExecutable(name string) (string, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("executable %q: %w", name, err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("executable %q: %w", resolved, err)
	}
	return absolute, nil
}

// setPseudoConsoleAttribute puts the pseudoconsole into a process attribute
// list.
//
// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE is one of the attributes whose value is
// the handle itself rather than a pointer to it: the documented call passes
// hpc and sizeof(hpc). UpdateProcThreadAttribute is therefore called directly
// instead of through the wrapper in x/sys/windows, whose value parameter is an
// unsafe.Pointer — a handle is not a pointer, and passing the address of one
// here compiles and then hands the child something it cannot use.
func setPseudoConsoleAttribute(list *windows.ProcThreadAttributeList, console windows.Handle) error {
	ret, _, err := syscall.SyscallN(procUpdateProcThreadAttr.Addr(),
		uintptr(unsafe.Pointer(list)),
		0, // flags, reserved and documented as zero
		uintptr(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE),
		uintptr(console),
		unsafe.Sizeof(console),
		0, // previous value, not wanted
		0, // returned size, not wanted
	)
	if ret == 0 {
		if err != 0 {
			return err
		}
		return errors.New("UpdateProcThreadAttribute failed")
	}
	return nil
}

// createKillOnCloseJob makes a job object that kills everything still in it
// when its last handle is closed.
func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	// The wrapper accepts uintptr, so pin the allocation across its Go code
	// and lazy symbol resolution as well as the final syscall.
	var pinned runtime.Pinner
	pinned.Pin(&limits)
	defer pinned.Unpin()
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// environmentBlock encodes the child's environment the way
// CREATE_UNICODE_ENVIRONMENT expects it: the entries, each terminated, with
// one more terminator closing the block.
func environmentBlock(extra []string) (*uint16, error) {
	block := make([]uint16, 0, 4096)
	for _, entry := range environmentEntries(os.Environ(), extra) {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, fmt.Errorf("environment entry %q: %w", entry, err)
		}
		block = append(block, encoded...) // UTF16FromString terminates each entry
	}
	block = append(block, 0)
	return &block[0], nil
}

// environmentEntries merges extra over base.
//
// Environment variable names are case-insensitive on Windows, so an entry
// replaces what is already there without regard to case; otherwise setting
// TERM in a process started with Term already set would produce two entries
// and leave which one the child sees to chance. CreateProcess documents the
// block as sorted by name, case-insensitively, which is the order returned
// here.
func environmentEntries(base, extra []string) []string {
	entries := make(map[string]string, len(base)+len(extra))
	add := func(entry string) {
		if name, ok := environmentName(entry); ok {
			entries[strings.ToUpper(name)] = entry
		}
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range extra {
		add(entry)
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)

	merged := make([]string, 0, len(names))
	for _, name := range names {
		merged = append(merged, entries[name])
	}
	return merged
}

// environmentName returns the variable name of a KEY=VALUE entry.
//
// Windows keeps the current directory of each drive in variables whose name
// starts with '=', such as "=C:=C:\\src", so a name ends at the first '=' that
// is not the first character. An entry with no '=' at all is not an entry.
func environmentName(entry string) (string, bool) {
	if entry == "" {
		return "", false
	}
	start := 0
	if entry[0] == '=' {
		start = 1
	}
	i := strings.IndexByte(entry[start:], '=')
	if i < 0 {
		return "", false
	}
	return entry[:start+i], true
}

// drain moves the program's output into the emulator until the channel breaks.
//
// This runs from before the program starts until teardown, including across
// the close of the pseudoconsole, which can emit a last frame that has to be
// read: an unread frame is a full pipe, and a full pipe is what makes closing
// a pseudoconsole hang.
func (tm *conptyTerminal) drain() {
	defer close(tm.drained)
	buffer := make([]byte, 32*1024)
	for {
		var read uint32
		if err := windows.ReadFile(tm.output, buffer, &read, nil); err != nil {
			return // the channel broke, or teardown cancelled the read
		}
		if read == 0 {
			return
		}
		_, _ = tm.emu.Write(buffer[:read])
	}
}

// forward moves the emulator's input side into the pseudoconsole.
//
// Two things travel this way. One is what a test sends: keys, text and pastes.
// The other is what the emulator answers to the program's queries — device
// attributes, cursor position, mode and colour reports — which it writes while
// it is parsing the output that asked. Those answers must move independently
// of anything the test is doing, because a program that queries the terminal
// before drawing anything is blocked until they arrive, and the goroutine
// parsing its output is blocked until they are taken.
func (tm *conptyTerminal) forward() {
	defer close(tm.forwarded)
	defer close(tm.inputQueue)
	buffer := make([]byte, inputChunkSize)
	overflow := false
	for {
		read, err := tm.emu.Read(buffer)
		if read > 0 && !overflow {
			chunk := append([]byte(nil), buffer[:read]...)
			select {
			case tm.inputQueue <- chunk:
			default:
				tm.failInput(errors.New("pseudoconsole input queue exceeded four MiB"))
				overflow = true
			}
		}
		// Keep draining after overflow so a producer never remains blocked
		// inside SafeEmulator's mutex. Capture and send operations report it.
		if err != nil {
			return
		}
	}
}

func (tm *conptyTerminal) writeQueuedInput() {
	defer close(tm.written)
	broken := false
	for data := range tm.inputQueue {
		if broken {
			continue
		}
		if err := tm.writeInput(data); err != nil {
			tm.failInput(err)
			broken = true
		}
	}
}

func (tm *conptyTerminal) failInput(err error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if !tm.closed && tm.inputErr == nil {
		tm.inputErr = err
	}
}

func (tm *conptyTerminal) inputFailure() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.inputErr
}

func (tm *conptyTerminal) writeInput(data []byte) error {
	for len(data) > 0 {
		var written uint32
		if err := windows.WriteFile(tm.input, data, &written, nil); err != nil {
			return err
		}
		if written == 0 {
			return errors.New("the pseudoconsole stopped accepting input")
		}
		data = data[written:]
	}
	return nil
}

func (tm *conptyTerminal) resize(width, height int) error {
	if err := checkSize(width, height); err != nil {
		return err
	}
	// The emulator is resized first. After the pseudoconsole is resized the
	// program redraws for the new size, and a frame drawn for a size the
	// emulator has not been given yet would be laid out against the old
	// geometry.
	tm.emu.Resize(width, height)
	if err := windows.ResizePseudoConsole(tm.console, windows.Coord{X: int16(width), Y: int16(height)}); err != nil {
		currentWidth, currentHeight := tm.knownSize()
		tm.emu.Resize(currentWidth, currentHeight)
		return fmt.Errorf("resizing the pseudoconsole to %dx%d: %w", width, height, err)
	}
	tm.mu.Lock()
	tm.width, tm.height = width, height
	tm.mu.Unlock()
	return nil
}

// size reports the size the program is being told.
//
// Windows offers no call that reads a pseudoconsole's size back, so what is
// reported is the size the console host was given. That is checked against the
// emulator, which was sized from the same numbers and is what interprets the
// program's output: a disagreement means this backend has a bug, and saying so
// is worth more than a confident wrong answer. Whether the program itself
// agrees is a separate question, and it is answered independently by the
// scenario that runs a child which reports the size it reads from its own
// console.
func (tm *conptyTerminal) size() (int, int, error) {
	width, height := tm.knownSize()
	if emulatorWidth, emulatorHeight := tm.emu.Width(), tm.emu.Height(); emulatorWidth != width || emulatorHeight != height {
		return 0, 0, fmt.Errorf("the emulator is %dx%d but the pseudoconsole was sized %dx%d",
			emulatorWidth, emulatorHeight, width, height)
	}
	return width, height, nil
}

func (tm *conptyTerminal) knownSize() (int, int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.width, tm.height
}

func checkSize(width, height int) error {
	if width <= 0 || height <= 0 || width > maxCell || height > maxCell {
		return fmt.Errorf("a terminal size must be between 1x1 and %dx%d, got %dx%d",
			maxCell, maxCell, width, height)
	}
	return nil
}

// sendKeys encodes each key the way the program's own negotiated modes say it
// should be encoded, which is what the emulator does: an arrow key becomes
// SS3 or CSI depending on whether the program turned on application cursor
// keys, and the same is true of the keypad.
//
// Every name is parsed before anything is sent, so a typo cannot leave half a
// sequence in the program's input.
func (tm *conptyTerminal) sendKeys(keys []string) error {
	events := make([]vt.KeyPressEvent, 0, len(keys))
	for _, name := range keys {
		event, err := parseKey(name)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	for _, event := range events {
		tm.emu.SendKey(event)
	}
	return tm.inputFailure()
}

func (tm *conptyTerminal) sendText(text string) error {
	tm.emu.SendText(text)
	return tm.inputFailure()
}

// paste sends text as a paste. The emulator brackets it when the program has
// turned bracketed paste on, and sends it plain when it has not, which is what
// a terminal does.
func (tm *conptyTerminal) paste(text string) error {
	tm.emu.Paste(text)
	return tm.inputFailure()
}

// capture returns the visible screen. When the program is in the alternate
// screen that is the alternate screen, and after the program has exited it is
// the last frame it drew, because nothing clears the emulator.
func (tm *conptyTerminal) capture() (string, error) {
	if err := tm.inputFailure(); err != nil {
		return "", err
	}
	return trimScreen(ansi.Strip(tm.emu.Render())), nil
}

func (tm *conptyTerminal) captureANSI() (string, error) {
	if err := tm.inputFailure(); err != nil {
		return "", err
	}
	return trimScreen(tm.emu.Render()), nil
}

func (tm *conptyTerminal) alive() bool {
	_, exited := tm.exitStatus()
	return !exited
}

// exitStatus returns the program's exit code, and whether it has exited.
//
// Waiting on the process with a zero timeout is the question "has it exited";
// the exit code is only meaningful once the answer is yes. Asking
// GetExitCodeProcess alone cannot answer it, because a program is free to exit
// with 259, the value that also means still running. Windows has no signals: a
// program killed by this harness reports the code the kill supplied, and one
// killed by the system reports the system's status code, so there is no
// signal-to-status translation of the kind Unix needs.
//
// The answer is remembered, so that a status read once stays readable after
// teardown has closed the process handle.
func (tm *conptyTerminal) exitStatus() (int, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.exited {
		return tm.code, true
	}
	if tm.process == 0 {
		return 0, false
	}
	event, err := windows.WaitForSingleObject(tm.process, 0)
	if err != nil || event != windows.WAIT_OBJECT_0 {
		return 0, false
	}
	var code uint32
	if err := windows.GetExitCodeProcess(tm.process, &code); err != nil {
		return 0, false
	}
	tm.code, tm.exited = int(code), true
	return tm.code, true
}

// unreapedReport has nothing to report: a Windows process that has exited
// always has its status available from its handle, so the state this exists
// for — the program is gone and no status will ever come — cannot happen here.
func (tm *conptyTerminal) unreapedReport() (string, bool) {
	return "", false
}

// close terminates the owned process tree and joins the communication workers.
// A cancellation timeout is reported as a test failure, not mistaken for a
// completed I/O operation: outstanding handles stay owned until their workers
// finish, with process exit as the final OS resource-release boundary.
func (tm *conptyTerminal) close() {
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return
	}
	tm.closed = true
	tm.mu.Unlock()

	_, _ = tm.exitStatus()
	if tm.job != 0 {
		_ = windows.TerminateJobObject(tm.job, 1)
	}
	if tm.process != 0 {
		// The process is killed as well as its job, and not only if the job
		// is missing: a program that never made it into the job — one that
		// failed to start, and is therefore still suspended — is in no job to
		// be killed by, and it is exactly the one that must not be left
		// behind. Terminating a process that has already exited fails
		// harmlessly and cannot change the status read above.
		_ = windows.TerminateProcess(tm.process, 1)
		_, _ = windows.WaitForSingleObject(tm.process, uint32(closeGrace/time.Millisecond))
		_, _ = tm.exitStatus()
	}

	if tm.console != 0 {
		windows.ClosePseudoConsole(tm.console)
		tm.console = 0
	}

	drained := tm.stopped(tm.drained, tm.output)
	if !drained {
		tm.t.Errorf("the goroutine draining the pseudoconsole output did not stop within %s", closeGrace)
	}
	// Closing only the pipe writer avoids Emulator.Close's unsynchronized
	// closed flag racing with the reader goroutine.
	_ = tm.inputPipe.Close()
	forwarded := tm.stopped(tm.forwarded, 0)
	if !forwarded {
		tm.t.Errorf("the emulator input reader did not stop within %s", closeGrace)
	}
	written := tm.stopped(tm.written, tm.input)
	if !written {
		tm.t.Errorf("the pseudoconsole input writer did not stop within %s", closeGrace)
	}
	if drained && forwarded && written {
		tm.closeHandles()
		return
	}
	// Never recycle numeric handles while a failed-to-cancel syscall may
	// still use them. This test is already failed; defer reclamation safely.
	go func() {
		<-tm.drained
		<-tm.forwarded
		<-tm.written
		tm.closeHandles()
	}()
}

// stopped waits for one of the channel goroutines to finish, cancelling the
// I/O it is blocked in if it does not finish on its own.
func (tm *conptyTerminal) stopped(done <-chan struct{}, handle windows.Handle) bool {
	select {
	case <-done:
		return true
	case <-time.After(closeGrace / 2):
	}
	// A synchronous read or write on a pipe cannot be interrupted, but it can
	// be cancelled: CancelIoEx cancels the operations in flight on a handle
	// whichever thread started them, and the cancelled call returns an error,
	// which is what ends the goroutine. Cancelling before closing is the point
	// of this: an operation still in flight when its handle is closed is left
	// working on a handle number the system is free to hand to something else,
	// so the handle is closed after the goroutine is gone whenever cancelling
	// achieves that.
	if handle != 0 {
		_ = windows.CancelIoEx(handle, nil)
	}
	select {
	case <-done:
		return true
	case <-time.After(closeGrace / 2):
		return false
	}
}

// closeHandles releases every handle this terminal holds.
//
// The order is the order in which they stop being useful: the pipes, then the
// child's process and thread, and the job last, because closing the last
// handle to the job kills anything still inside it and that is the backstop
// for a child that survived everything else.
func (tm *conptyTerminal) closeHandles() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, handle := range []*windows.Handle{&tm.output, &tm.input, &tm.thread, &tm.process, &tm.job} {
		if *handle != 0 {
			_ = windows.CloseHandle(*handle)
			*handle = 0
		}
	}
}

// modifierPrefixes are the modifier prefixes a key name may carry, spelled the
// way tmux spells them so that a test reads the same on either platform.
var modifierPrefixes = map[byte]vt.KeyMod{
	'C': vt.ModCtrl, 'c': vt.ModCtrl,
	'M': vt.ModAlt, 'm': vt.ModAlt,
	'S': vt.ModShift, 's': vt.ModShift,
}

// specialKeys are the key names that are not a single character. tmux's names
// come first because the tests were written against them; the spellings people
// reach for are accepted too.
var specialKeys = map[string]rune{
	"enter":     vt.KeyEnter,
	"return":    vt.KeyEnter,
	"space":     vt.KeySpace,
	"escape":    vt.KeyEscape,
	"esc":       vt.KeyEscape,
	"tab":       vt.KeyTab,
	"bspace":    vt.KeyBackspace,
	"backspace": vt.KeyBackspace,
	"bs":        vt.KeyBackspace,
	"up":        vt.KeyUp,
	"down":      vt.KeyDown,
	"left":      vt.KeyLeft,
	"right":     vt.KeyRight,
	"home":      vt.KeyHome,
	"end":       vt.KeyEnd,
	"ppage":     vt.KeyPgUp,
	"pageup":    vt.KeyPgUp,
	"pgup":      vt.KeyPgUp,
	"npage":     vt.KeyPgDown,
	"pagedown":  vt.KeyPgDown,
	"pgdn":      vt.KeyPgDown,
	"ic":        vt.KeyInsert,
	"insert":    vt.KeyInsert,
	"dc":        vt.KeyDelete,
	"delete":    vt.KeyDelete,
	"del":       vt.KeyDelete,
	"f1":        vt.KeyF1,
	"f2":        vt.KeyF2,
	"f3":        vt.KeyF3,
	"f4":        vt.KeyF4,
	"f5":        vt.KeyF5,
	"f6":        vt.KeyF6,
	"f7":        vt.KeyF7,
	"f8":        vt.KeyF8,
	"f9":        vt.KeyF9,
	"f10":       vt.KeyF10,
	"f11":       vt.KeyF11,
	"f12":       vt.KeyF12,
}

// parseKey turns a key name into the key event the emulator encodes.
func parseKey(name string) (vt.KeyPressEvent, error) {
	var none vt.KeyPressEvent
	if name == "" {
		return none, errors.New("a key name cannot be empty")
	}

	var mod vt.KeyMod
	rest := name
	for len(rest) > 2 && rest[1] == '-' {
		prefix, ok := modifierPrefixes[rest[0]]
		if !ok {
			break
		}
		mod |= prefix
		rest = rest[2:]
	}

	if utf8.RuneCountInString(rest) == 1 {
		key, _ := utf8.DecodeRuneInString(rest)
		return characterKey(name, key, mod)
	}
	key, ok := specialKeys[strings.ToLower(rest)]
	if !ok {
		return none, fmt.Errorf("unknown key name %q: SendKeys takes key names, SendText takes literal text", name)
	}
	if unicode.IsPrint(key) {
		// "Space" is a name for a character, so it is encoded as one.
		return characterKey(name, key, mod)
	}
	// A modifier on a named key is only sent where a terminal has a sequence
	// for it: alt prefixes an escape, and shift with tab is the back tab.
	// Anything else has no encoding, and sending it would put nothing at all
	// into the program's input, which is the failure that looks like a bug in
	// the program.
	switch {
	case mod&^vt.ModAlt == 0:
	case key == vt.KeyTab && mod == vt.ModShift:
	default:
		return none, fmt.Errorf("%q has no encoding a terminal can send", name)
	}
	return vt.KeyPressEvent{Code: key, Mod: mod}, nil
}

// characterKey builds the event for a printable character.
func characterKey(name string, key rune, mod vt.KeyMod) (vt.KeyPressEvent, error) {
	// Shift on a character is the shifted character: a terminal transmits "G",
	// not shift and "g", and the shifted form is the only way to type it.
	if mod&vt.ModShift != 0 {
		shifted := unicode.ToUpper(key)
		if shifted == key {
			// Which character shift produces here is a property of the
			// keyboard layout, not of the terminal, and guessing it would send
			// the unshifted character while claiming otherwise. The caller
			// knows which character they meant.
			return vt.KeyPressEvent{}, fmt.Errorf("%q: shift on %q depends on the keyboard layout, so send the shifted character itself", name, string(key))
		}
		key = shifted
		mod &^= vt.ModShift
	}
	if mod&vt.ModCtrl != 0 {
		key = unicode.ToLower(key)
		if !hasControlEncoding(key) {
			return vt.KeyPressEvent{}, fmt.Errorf("%q has no encoding a terminal can send", name)
		}
	}
	if mod&^(vt.ModAlt|vt.ModCtrl) != 0 {
		return vt.KeyPressEvent{}, fmt.Errorf("%q has no encoding a terminal can send", name)
	}
	// Text is deliberately left empty: the emulator matches a key event by
	// value, and a populated Text field would stop it matching the encodings
	// it knows.
	return vt.KeyPressEvent{Code: key, Mod: mod}, nil
}

// hasControlEncoding reports whether control with this character is a byte a
// terminal can send. The control codes are the top left corner of ASCII: the
// letters, the five characters that follow them, and space, which is NUL.
func hasControlEncoding(key rune) bool {
	switch {
	case key >= 'a' && key <= 'z':
		return true
	case key == '[', key == '\\', key == ']', key == '^', key == '_', key == ' ':
		return true
	}
	return false
}
