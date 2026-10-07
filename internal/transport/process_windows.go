//go:build windows

package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// managedProcess owns an OpenSSH process and its Windows job. Closing the job
// kills every process OpenSSH started, including ProxyCommand and ProxyJump.
type managedProcess struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	Done   <-chan struct{}

	pid        uint32
	job        windows.Handle
	process    windows.Handle
	inputFile  *os.File
	outputFile *os.File
	stderrDone chan struct{}
	done       chan struct{}
	waitErr    error
	closeErr   error
	exitCode   uint32
	mu         sync.Mutex
	closeOnce  sync.Once
}

func (p *managedProcess) PID() uint32      { return p.pid }
func (p *managedProcess) ExitCode() uint32 { <-p.done; return p.exitCode }

func (p *managedProcess) Wait() error {
	<-p.done
	return p.waitErr
}

type jobAccountingInfo struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount                                                                uint32
	TotalProcesses, ActiveProcesses, TotalTerminatedProcesses                          uint32
}

func (p *managedProcess) terminateAndReapJob() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job == 0 || p.job == windows.InvalidHandle {
		return nil
	}
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		_ = windows.CloseHandle(p.job)
		p.job = windows.InvalidHandle
		return fmt.Errorf("terminate OpenSSH process job: %w", err)
	}
	for {
		var info jobAccountingInfo
		if err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			_ = windows.CloseHandle(p.job)
			p.job = windows.InvalidHandle
			return fmt.Errorf("query OpenSSH process job: %w", err)
		}
		if info.ActiveProcesses == 0 {
			_ = windows.CloseHandle(p.job)
			p.job = windows.InvalidHandle
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *managedProcess) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		if p.job != 0 && p.job != windows.InvalidHandle {
			if err := windows.TerminateJobObject(p.job, 1); err != nil {
				p.closeErr = fmt.Errorf("terminate OpenSSH process job: %w", err)
				if closeErr := windows.CloseHandle(p.job); closeErr != nil {
					p.closeErr = errors.Join(p.closeErr, fmt.Errorf("close OpenSSH process job: %w", closeErr))
				}
				p.job = windows.InvalidHandle
			}
		}
		p.mu.Unlock()
		if p.inputFile != nil {
			_ = p.inputFile.Close()
		}
		_ = p.Wait()
		if p.outputFile != nil {
			_ = p.outputFile.Close()
		}
		if p.process != 0 && p.process != windows.InvalidHandle {
			_ = windows.CloseHandle(p.process)
			p.process = windows.InvalidHandle
		}
	})
	return errors.Join(p.waitErr, p.closeErr)
}

func startManaged(ctx context.Context, executable string, args []string, pipeInput bool, stderr io.Writer) (*managedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var childIn, parentIn, childOut, parentOut windows.Handle
	var err error
	if pipeInput {
		childIn, parentIn, err = managedPipe(false)
		if err != nil {
			return nil, err
		}
	} else {
		childIn, err = managedNullInput()
		if err != nil {
			return nil, err
		}
	}
	parentOut, childOut, err = managedPipe(true)
	if err != nil {
		closeHandle(childIn)
		closeHandle(parentIn)
		return nil, err
	}
	parentErr, childErr, err := managedPipe(true)
	if err != nil {
		closeHandle(childIn)
		closeHandle(parentIn)
		closeHandle(childOut)
		closeHandle(parentOut)
		return nil, err
	}
	cleanHandles := func() {
		for _, h := range []windows.Handle{childIn, parentIn, childOut, parentOut, childErr, parentErr} {
			closeHandle(h)
		}
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		cleanHandles()
		return nil, err
	}
	jobInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	jobInfo.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	defer attrs.Delete()
	handles := [3]windows.Handle{childIn, childOut, childErr}
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(unsafe.Sizeof(handles))); err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = childIn, childOut, childErr
	startup.ProcThreadAttributeList = attrs.List()

	cmdArgs := append([]string{executable}, args...)
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(cmdArgs))
	if err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	app, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	env := managedEnvironment(ctx)
	environment := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW)
	err = windows.CreateProcess(app, &commandLine[0], nil, nil, true, flags, &environment[0], nil, &startup.StartupInfo, &pi)
	if err != nil {
		closeHandle(job)
		cleanHandles()
		return nil, err
	}
	closeHandle(childIn)
	childIn = windows.InvalidHandle
	closeHandle(childOut)
	childOut = windows.InvalidHandle
	closeHandle(childErr)
	childErr = windows.InvalidHandle
	defer closeHandle(pi.Thread)
	started := false
	defer func() {
		if !started {
			_ = windows.TerminateProcess(pi.Process, 1)
			_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
			closeHandle(pi.Process)
			closeHandle(job)
			cleanHandles()
		}
	}()
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		return nil, err
	}
	started = true

	input := (*os.File)(nil)
	if pipeInput {
		input = os.NewFile(uintptr(parentIn), "ssh-stdin")
		parentIn = windows.InvalidHandle
	}
	output := os.NewFile(uintptr(parentOut), "ssh-stdout")
	parentOut = windows.InvalidHandle
	stderrFile := os.NewFile(uintptr(parentErr), "ssh-stderr")
	parentErr = windows.InvalidHandle
	p := &managedProcess{pid: pi.ProcessId, job: job, process: pi.Process, inputFile: input, outputFile: output, Input: input, Output: output, done: make(chan struct{}), stderrDone: make(chan struct{})}
	p.Done = p.done
	stopCancellation := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Close()
		case <-p.done:
		case <-stopCancellation:
		}
	}()
	go func() {
		defer close(p.stderrDone)
		if stderr == nil {
			_, _ = io.Copy(io.Discard, stderrFile)
		} else {
			_, _ = io.Copy(stderr, stderrFile)
		}
		_ = stderrFile.Close()
	}()
	go func() {
		_, waitErr := windows.WaitForSingleObject(p.process, windows.INFINITE)
		p.waitErr = waitErr
		if waitErr == nil {
			_ = windows.GetExitCodeProcess(p.process, &p.exitCode)
		}
		// Closing the job on natural root exit also terminates any orphaned
		// proxy process that inherited the stdio handles.
		if err := p.terminateAndReapJob(); p.waitErr == nil {
			p.waitErr = err
		}
		if p.inputFile != nil {
			_ = p.inputFile.Close()
		}
		<-p.stderrDone
		close(p.done)
		close(stopCancellation)
	}()
	return p, nil
}

func managedPipe(parentReads bool) (windows.Handle, windows.Handle, error) {
	var read, write windows.Handle
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&read, &write, sa, 0); err != nil {
		return 0, 0, err
	}
	parent := read
	if !parentReads {
		parent = write
	}
	if err := windows.SetHandleInformation(parent, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		closeHandle(read)
		closeHandle(write)
		return 0, 0, err
	}
	return read, write, nil
}

func managedNullInput() (windows.Handle, error) {
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	return windows.CreateFile(windows.StringToUTF16Ptr("NUL"), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, windows.OPEN_EXISTING, 0, 0)
}

func closeHandle(handle windows.Handle) {
	if handle != 0 && handle != windows.InvalidHandle {
		_ = windows.CloseHandle(handle)
	}
}

func hideWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
func nullConfig() string        { return "NUL" }
func supportsConfigProxy() bool { return true }
