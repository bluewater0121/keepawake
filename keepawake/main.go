//go:build windows

// Command keepawake 是一个 Windows 防休眠小工具：
//   - 运行期间阻止系统进入睡眠/休眠（SetThreadExecutionState）；
//   - 默认允许显示器按电源计划自动关闭（不设置 ES_DISPLAY_REQUIRED）；
//   - 也可通过 -display-off 启动后立即关闭显示器，或 -keep-display
//     进入"连显示器一起保住"的 caffeine 模式。
//
// 用法：
//
//	keepawake                      # 系统不休眠，显示器可自动关闭
//	keepawake -display-off         # 启动后立即关显示器（系统保持唤醒）
//	keepawake -keep-display        # caffeine 模式，系统+显示器都常亮
//	keepawake -duration 2h         # 运行 2 小时后自动退出
//	keepawake -away                # Awaymode，系统表现为"离开"但不睡眠
//
// 原理：
//   - kernel32!SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)
//     在调用线程存活期间持续向电源管理器声明"系统正在使用"，
//     从而阻止空闲睡眠；不传 ES_DISPLAY_REQUIRED 时显示器仍可断电。
//   - user32!SendMessageW(HWND_BROADCAST, WM_SYSCOMMAND, SC_MONITORPOWER, 2)
//     广播关闭显示器；-1 为开启，1 为低功耗，2 为关闭。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// 执行状态标志（WinNT.h）
const (
	esContinuous       uint32 = 0x80000000 // 状态持续生效，直到显式复位或线程结束
	esSystemRequired   uint32 = 0x00000001 // 阻止系统进入睡眠
	esDisplayRequired  uint32 = 0x00000002 // 阻止显示器关闭（本工具默认不使用）
	esAwaymodeRequired uint32 = 0x00000040 // Awaymode：不睡眠且对外表现为离开
)

// 显示器电源控制（WinUser.h）
const (
	hwndBroadcast  uintptr = 0xFFFF  // 广播给所有顶层窗口
	wmSyscommand   uint32  = 0x0112  // WM_SYSCOMMAND
	scMonitorpower uint32  = 0xF170  // SC_MONITORPOWER
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procSetExecState = kernel32.NewProc("SetThreadExecutionState")
	user32           = syscall.NewLazyDLL("user32.dll")
	procSendMessageW = user32.NewProc("SendMessageW")
)

// setExecutionState 调用 SetThreadExecutionState。
// 返回值为 0 表示失败，非 0 表示成功（返回上一个状态）。
func setExecutionState(state uint32) error {
	r, _, err := procSetExecState.Call(uintptr(state))
	if r == 0 {
		return err
	}
	return nil
}

// setMonitorPower 向所有顶层窗口广播显示器电源控制：
// 2 = 关闭，1 = 低功耗，-1 = 开启。
func setMonitorPower(mode int) {
	procSendMessageW.Call(hwndBroadcast, uintptr(wmSyscommand), uintptr(scMonitorpower), uintptr(mode))
}

func main() {
	// 执行状态是"线程级"的：Go 运行时可能把 goroutine 迁移到其他
	// OS 线程，原线程一旦被回收状态就会丢失。必须锁定 OS 线程。
	runtime.LockOSThread()

	displayOff := flag.Bool("display-off", false, "启动后立即关闭显示器（系统保持唤醒）")
	keepDisplay := flag.Bool("keep-display", false, "同时保持显示器常亮（caffeine 模式）")
	away := flag.Bool("away", false, "使用 ES_AWAYMODE_REQUIRED（系统表现为离开但不睡眠）")
	duration := flag.Duration("duration", 0, "运行时长，如 30m、2h；为 0 则一直运行")
	flag.Parse()

	if *displayOff && *keepDisplay {
		fmt.Fprintln(os.Stderr, "错误: -display-off 与 -keep-display 不能同时使用")
		os.Exit(2)
	}

	state := esContinuous | esSystemRequired
	if *keepDisplay {
		state |= esDisplayRequired
	}
	if *away {
		state |= esAwaymodeRequired
	}
	if err := setExecutionState(state); err != nil {
		fmt.Fprintln(os.Stderr, "错误: SetThreadExecutionState 失败:", err)
		os.Exit(1)
	}

	switch {
	case *keepDisplay:
		fmt.Println("[keepawake] 模式: 系统 + 显示器均保持唤醒")
	case *away:
		fmt.Println("[keepawake] 模式: 系统保持唤醒（Awaymode），显示器可自动关闭")
	default:
		fmt.Println("[keepawake] 模式: 系统保持唤醒，显示器按电源计划自动关闭")
	}

	if *displayOff {
		// 稍等片刻，确保执行状态已向电源管理器注册
		time.Sleep(300 * time.Millisecond)
		setMonitorPower(2)
		fmt.Println("[keepawake] 已广播关闭显示器指令（移动鼠标或按键即可唤醒了）")
	}

	if *duration > 0 {
		fmt.Printf("[keepawake] 将运行 %s（约 %s 自动退出，Ctrl+C 可提前退出）\n",
			*duration, time.Now().Add(*duration).Format("15:04:05"))
	} else {
		fmt.Println("[keepawake] 持续运行中，按 Ctrl+C 退出")
	}
	fmt.Println("[keepawake] 提示: 本工具不阻止屏幕保护/锁屏；如锁屏请调整电源计划")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	start := time.Now()
	var timeout <-chan time.Time
	if *duration > 0 {
		timeout = time.After(*duration)
	}

	select {
	case <-sig:
		fmt.Println("\n[keepawake] 收到退出信号")
	case <-timeout:
		fmt.Println("\n[keepawake] 到达设定时长")
	}

	// 显式复位执行状态（进程退出时系统也会自动清除，此处稳妥起见）
	_ = setExecutionState(esContinuous)
	fmt.Printf("[keepawake] 已恢复默认电源行为，本次运行 %s\n", time.Since(start).Round(time.Second))
}
