// 検証用の子プロセス。実際の機密値や外部サービスは使用しない。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const marker = "試験専用の非公開値"

func main() {
	if len(os.Args) < 2 {
		os.Exit(90)
	}
	switch os.Args[1] {
	case "contract":
		want := []string{"", "空白 日本語", `引用"符`, `末尾\`, `空白 末尾\`, "--json", "--", marker}
		dir, _ := os.Getwd()
		if !reflect.DeepEqual(os.Args[2:], want) || dir != os.Getenv("CHECKSTAMP_TEST_DIRECTORY") || os.Getenv("CHECKSTAMP_TEST_VALUE") != marker {
			os.Exit(91)
		}
		fmt.Fprint(os.Stdout, marker+"-標準出力")
		fmt.Fprint(os.Stderr, marker+"-標準エラー")
	case "exit":
		code, _ := strconv.Atoi(os.Args[2])
		os.Exit(code)
	case "change":
		must(os.WriteFile("src/追加", []byte(marker), 0600))
		must(os.WriteFile("src/変更", []byte(marker), 0600))
		must(os.Remove("src/削除"))
		code, _ := strconv.Atoi(os.Args[2])
		os.Exit(code)
	case "read-failure":
		must(os.Remove("src/変更"))
		must(os.Symlink(os.Args[0], "src/変更"))
	case "save-failure":
		// 自分の開始記録だけを読取専用にし、完了時の名前置換を失敗させる。
		files, err := filepath.Glob(".checkstamp/runs/*.json")
		must(err)
		if len(files) != 1 {
			os.Exit(92)
		}
		must(os.Chmod(files[0], 0400))
		os.Exit(64)
	case "volume":
		block := strings.Repeat("x", 32*1024)
		for range 256 {
			fmt.Fprint(os.Stdout, block)
			fmt.Fprint(os.Stderr, block)
		}
	case "tree", "tree-ignore", "tree-exit", "tree-exit-many":
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt)
		self, _ := os.Executable()
		count := 1
		if os.Args[1] == "tree-exit-many" {
			count = 32
		}
		for index := range count {
			inputFile, readyFile, pidFile := "src/変更", "leaf-ready", "grandchild.pid"
			if count > 1 {
				inputFile = filepath.Join("src", "保持-"+strconv.Itoa(index))
				readyFile = "leaf-ready-" + strconv.Itoa(index)
				if index > 0 {
					pidFile = "grandchild-" + strconv.Itoa(index) + ".pid"
				}
			}
			child := exec.Command(self, "leaf", inputFile, readyFile)
			// 意図的に出力ハンドルを継承させ、直接の子だけを待つ実装の停止を検出する。
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			must(child.Start())
			waitFor(readyFile)
			must(os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0600))
		}
		must(os.WriteFile("child.pid", []byte(strconv.Itoa(os.Getpid())), 0600))
		must(os.WriteFile("ready", []byte("準備完了"), 0600))
		if os.Args[1] == "tree-exit" || os.Args[1] == "tree-exit-many" {
			return
		}
		for {
			<-signals
			must(os.WriteFile("notified", []byte("中断受信"), 0600))
			if os.Args[1] == "tree" {
				os.Exit(2)
			}
		}
	case "leaf":
		signal.Ignore(os.Interrupt)
		name, _ := syscall.UTF16PtrFromString(os.Args[2])
		handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		must(err)
		defer syscall.CloseHandle(handle)
		must(os.WriteFile(os.Args[3], []byte("準備完了"), 0600))
		for {
			time.Sleep(time.Second)
		}
	case "console":
		// 親の試験プロセスにコンソールがなくても、中断試験専用のコンソールを用意する。
		kernel := syscall.NewLazyDLL("kernel32.dll")
		kernel.NewProc("FreeConsole").Call()
		ok, _, _ := kernel.NewProc("AllocConsole").Call()
		if ok == 0 {
			os.Exit(93)
		}
		// AllocConsoleは既存のハンドラを解除するため、試験用の親だけで通知を受け止める。
		// NULLのハンドラで無視を設定すると子へ継承されるので使用しない。
		kernel.NewProc("SetConsoleCtrlHandler").Call(syscall.NewCallback(func(uint32) uintptr { return 1 }), 1)
		kernel.NewProc("SetConsoleCtrlHandler").Call(0, 0)
		window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
		syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(window, 0)
		pidFile, mode := os.Args[2], os.Args[3]
		child := exec.Command(os.Args[4], os.Args[5:]...)
		if mode != "ctrl-c" {
			child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
		}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		must(child.Start())
		watchdog := time.AfterFunc(12*time.Second, func() { child.Process.Kill() })
		defer watchdog.Stop()
		must(os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0600))
		if mode == "early" {
			// 開始記録の公開直後に通知し、子がまだ起動していない場合も対象にする。
			for {
				files, _ := filepath.Glob(".checkstamp/runs/*.json")
				if len(files) > 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
		} else {
			waitFor("ready")
		}
		if mode == "kill" {
			must(child.Process.Kill())
		} else {
			event, group := uintptr(1), uintptr(child.Process.Pid)
			if mode == "ctrl-c" {
				event, group = 0, 0
			}
			ok, _, _ := kernel.NewProc("GenerateConsoleCtrlEvent").Call(event, group)
			if ok == 0 {
				os.Exit(94)
			}
			if mode == "repeat" {
				waitFor("notified")
				ok, _, _ := kernel.NewProc("GenerateConsoleCtrlEvent").Call(event, group)
				if ok == 0 {
					os.Exit(94)
				}
			}
		}
		err := child.Wait()
		code := 0
		if err != nil {
			code = child.ProcessState.ExitCode()
		}
		// コンソールを解放してから結果を返す。ハンドルは試験側のパイプを使用する。
		kernel.NewProc("FreeConsole").Call()
		os.Exit(code)
	default:
		os.Exit(95)
	}
}

func must(err error) {
	if err != nil {
		// OSエラーや引数の本文は表示しない。
		os.Exit(96)
	}
}

func waitFor(name string) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(97)
}
