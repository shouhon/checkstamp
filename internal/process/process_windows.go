// Package process は、Windowsのジョブ内で実行ファイルとその子孫を管理する。
package process

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	ErrStart             = errors.New("実行ファイルを管理対象として起動できませんでした。")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	createJob            = kernel32.NewProc("CreateJobObjectW")
	setJob               = kernel32.NewProc("SetInformationJobObject")
	terminateJob         = kernel32.NewProc("TerminateJobObject")
	queryJob             = kernel32.NewProc("QueryInformationJobObject")
	isProcessInJob       = kernel32.NewProc("IsProcessInJob")
	initializeAttributes = kernel32.NewProc("InitializeProcThreadAttributeList")
	updateAttribute      = kernel32.NewProc("UpdateProcThreadAttribute")
	deleteAttributes     = kernel32.NewProc("DeleteProcThreadAttributeList")
	generateControl      = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

type jobLimits struct {
	ProcessTime, JobTime                                       int64
	Flags                                                      uint32
	MinWorkingSet, MaxWorkingSet                               uintptr
	ActiveProcesses                                            uint32
	Affinity                                                   uintptr
	Priority, Scheduling                                       uint32
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

type startupInfoEx struct {
	syscall.StartupInfo
	Attributes *byte
}

type jobAccounting struct {
	Times                                 [4]int64
	PageFaults, Total, Active, Terminated uint32
}

// ジョブの一覧が変化した場合は、取りこぼした子孫の終了を確認済みにしない。
func openJobProcesses(job uintptr, until time.Time) ([]syscall.Handle, bool) {
	capacity := uint32(16)
	for time.Now().Before(until) {
		size := uint64(8) + uint64(capacity)*uint64(unsafe.Sizeof(uintptr(0)))
		if size > uint64(^uint32(0)) || size > uint64(^uint(0)>>1) {
			return nil, false
		}
		buffer := make([]byte, int(size))
		ok, _, callErr := queryJob.Call(job, 3, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if ok == 0 && callErr != syscall.ERROR_MORE_DATA {
			return nil, false
		}
		assigned := binary.LittleEndian.Uint32(buffer[:4])
		count := binary.LittleEndian.Uint32(buffer[4:8])
		if assigned > count || ok == 0 {
			if assigned <= capacity {
				return nil, false
			}
			capacity = assigned
			continue
		}
		if count > capacity {
			return nil, false
		}
		handles := make([]syscall.Handle, 0, count)
		ids := unsafe.Slice((*uintptr)(unsafe.Pointer(&buffer[8])), count)
		for _, pid := range ids {
			if pid == 0 || uint64(pid) > uint64(^uint32(0)) {
				return handles, false
			}
			handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE|0x1000, false, uint32(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
			if err != nil {
				return handles, false
			}
			var belongs int32
			ok, _, _ := isProcessInJob.Call(uintptr(handle), job, uintptr(unsafe.Pointer(&belongs)))
			if ok == 0 || belongs == 0 {
				syscall.CloseHandle(handle)
				return handles, false
			}
			handles = append(handles, handle)
		}
		return handles, true
	}
	return nil, false
}

func readJobAccounting(job uintptr) (jobAccounting, bool) {
	var accounting jobAccounting
	ok, _, _ := queryJob.Call(job, 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
	return accounting, ok != 0
}

// Result は起動の有無、取得できた終了コードと中断の事実を返す。
type Result struct {
	Started       bool
	ExitCode      *int
	Interrupted   bool
	OutputFailed  bool
	CleanupFailed bool
}

// Resolve は通常の実行ファイルを検索し、絶対パスを返す。シェルは補わない。
func Resolve(name string) (string, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", ErrStart
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".exe" && ext != ".com" {
		return "", ErrStart
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", ErrStart
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrStart
	}
	return resolved, nil
}

type outputStream struct {
	file   *os.File
	reader *os.File
	owned  bool
	done   chan error
}

func openOutput(writer io.Writer, mu *sync.Mutex) (outputStream, error) {
	if file, ok := writer.(*os.File); ok {
		return outputStream{file: file}, nil
	}
	reader, writerFile, err := os.Pipe()
	if err != nil {
		return outputStream{}, ErrStart
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(lockedWriter{writer, mu}, reader)
		reader.Close()
		done <- err
	}()
	return outputStream{file: writerFile, reader: reader, owned: true, done: done}, nil
}

type lockedWriter struct {
	writer io.Writer
	mu     *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

// Execute は作成時からジョブへ所属させる。親の強制終了でもジョブの子孫を終了する。
// 中断はCTRL_BREAKで通知し、2秒後または再度の中断でジョブ全体を終了する。
func Execute(executable string, args []string, directory string, stdout, stderr io.Writer, signals <-chan os.Signal) (result Result) {
	job, _, _ := createJob.Call(0, 0)
	if job == 0 {
		return result
	}
	jobClosed := false
	defer func() {
		if !jobClosed {
			syscall.CloseHandle(syscall.Handle(job))
		}
	}()
	limits := jobLimits{Flags: 0x2000} // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if ok, _, _ := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return result
	}

	var mu sync.Mutex
	out, err := openOutput(stdout, &mu)
	if err != nil {
		return result
	}
	streams := []outputStream{out}
	defer func() {
		for _, stream := range streams {
			if stream.owned {
				stream.file.Close()
				if result.CleanupFailed {
					stream.reader.Close()
				}
				if <-stream.done != nil {
					result.OutputFailed = true
				}
			}
		}
	}()
	errout, err := openOutput(stderr, &mu)
	if err != nil {
		return result
	}
	streams = append(streams, errout)
	// 標準入力のハンドルも引き継ぐ。標準入力がない環境ではNULを渡す。
	stdin := os.Stdin
	if _, err := stdin.Stat(); err != nil {
		stdin, err = os.Open(os.DevNull)
		if err != nil {
			return result
		}
		defer stdin.Close()
	}
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return result
	}
	handles := make([]syscall.Handle, 3)
	defer func() {
		for _, handle := range handles {
			if handle != 0 {
				syscall.CloseHandle(handle)
			}
		}
	}()
	for i, file := range []*os.File{stdin, out.file, errout.file} {
		if syscall.DuplicateHandle(current, syscall.Handle(file.Fd()), current, &handles[i], 0, true, syscall.DUPLICATE_SAME_ACCESS) != nil {
			return result
		}
	}
	var size uintptr
	initializeAttributes.Call(0, 2, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return result
	}
	attributes := make([]byte, size)
	if ok, _, _ := initializeAttributes.Call(uintptr(unsafe.Pointer(&attributes[0])), 2, 0, uintptr(unsafe.Pointer(&size))); ok == 0 {
		return result
	}
	defer deleteAttributes.Call(uintptr(unsafe.Pointer(&attributes[0])))
	// 引き継ぐハンドルを標準入出力に限定し、ジョブのハンドルを子へ渡さない。
	if ok, _, _ := updateAttribute.Call(uintptr(unsafe.Pointer(&attributes[0])), 0, 0x20002, uintptr(unsafe.Pointer(&handles[0])), uintptr(len(handles))*unsafe.Sizeof(handles[0]), 0, 0); ok == 0 {
		return result
	}
	if ok, _, _ := updateAttribute.Call(uintptr(unsafe.Pointer(&attributes[0])), 0, 0x2000d, uintptr(unsafe.Pointer(&job)), unsafe.Sizeof(job), 0, 0); ok == 0 {
		return result
	}
	application, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return result
	}
	command := syscall.EscapeArg(executable)
	for _, arg := range args {
		command += " " + syscall.EscapeArg(arg)
	}
	commandLine, err := syscall.UTF16FromString(command)
	if err != nil || len(commandLine) > 32767 {
		return result
	}
	workingDirectory, err := syscall.UTF16PtrFromString(directory)
	if err != nil {
		return result
	}
	si := startupInfoEx{Attributes: &attributes[0]}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]
	var pi syscall.ProcessInformation
	select {
	case <-signals:
		result.Interrupted = true
		return result
	default:
	}
	err = syscall.CreateProcess(application, &commandLine[0], nil, nil, true, 0x80000|syscall.CREATE_NEW_PROCESS_GROUP, nil, workingDirectory, &si.StartupInfo, &pi)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(job)
	if err != nil {
		return result
	}
	result.Started = true
	syscall.CloseHandle(pi.Thread)
	defer syscall.CloseHandle(pi.Process)
	// 親側の書込ハンドルはここで閉じ、子孫終了後にコピー処理がEOFへ進めるようにする。
	for i, handle := range handles {
		syscall.CloseHandle(handle)
		handles[i] = 0
	}
	for _, stream := range streams {
		if stream.owned {
			stream.file.Close()
		}
	}

	var deadline <-chan time.Time
	var timer *time.Timer
	var cleanupHandles []syscall.Handle
	var cleanupTotal uint32
	cleanupStarted := false
	defer func() {
		for _, handle := range cleanupHandles {
			syscall.CloseHandle(handle)
		}
	}()
	terminateManaged := func() {
		before, beforeOK := readJobAccounting(job)
		if cleanupStarted && before.Total != cleanupTotal {
			result.CleanupFailed = true
		}
		if !cleanupStarted {
			cleanupTotal = before.Total
			cleanupStarted = true
		}
		handles, opened := openJobProcesses(job, time.Now().Add(5*time.Second))
		cleanupHandles = append(cleanupHandles, handles...)
		ok, _, _ := terminateJob.Call(job, 125)
		after, afterOK := readJobAccounting(job)
		if !beforeOK || !opened || ok == 0 || !afterOK || cleanupTotal != after.Total {
			result.CleanupFailed = true
		}
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-signals:
			if result.Interrupted {
				terminateManaged()
			} else {
				result.Interrupted = true
				generateControl.Call(1, uintptr(pi.ProcessId)) // CTRL_BREAK_EVENT
				timer = time.NewTimer(2 * time.Second)
				deadline = timer.C
			}
		case <-deadline:
			terminateManaged()
			deadline = nil
		default:
		}
		state, err := syscall.WaitForSingleObject(pi.Process, 20)
		if err != nil || state == syscall.WAIT_OBJECT_0 {
			var code uint32
			if err == nil && syscall.GetExitCodeProcess(pi.Process, &code) == nil {
				exit := int(code)
				result.ExitCode = &exit
			}
			break
		}
	}
	// 稼働数の減少だけでなく、終了要求前に保持した各プロセスの終了も待つ。
	terminateManaged()
	until := time.Now().Add(5 * time.Second)
	for _, handle := range cleanupHandles {
		remaining := time.Until(until).Milliseconds()
		if remaining <= 0 {
			result.CleanupFailed = true
			break
		}
		state, err := syscall.WaitForSingleObject(handle, uint32(remaining))
		if err != nil || state != syscall.WAIT_OBJECT_0 {
			result.CleanupFailed = true
			break
		}
	}
	if !result.CleanupFailed {
		for {
			accounting, ok := readJobAccounting(job)
			if !ok || accounting.Total != cleanupTotal || time.Now().After(until) {
				result.CleanupFailed = true
				break
			}
			if accounting.Active == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if result.CleanupFailed {
		// 終了確認が失敗しても、残ったジョブのハンドルを保持したまま出力を待たない。
		syscall.CloseHandle(syscall.Handle(job))
		jobClosed = true
	}
	return result
}
