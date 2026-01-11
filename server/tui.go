package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type TUI struct {
	app   *tview.Application
	pages *tview.Pages

	// Elementi prikaza
	statusBar   *tview.TextView
	usersTable  *tview.Table
	topicsTable *tview.Table
	chainInfo   *tview.TextView
	logsView    *tview.TextView

	logs []string
}

func RunTUI() {
	tui := &TUI{
		app:  tview.NewApplication(),
		logs: make([]string, 0),
	}

	tui.app.EnableMouse(true)

	tui.setupUI()

	go tui.updater()

	if err := tui.app.Run(); err != nil {
		panic(err)
	}

	// Safe close - only close if not already stopping
	if !cntrlldp.should_stop.Swap(true) {
		select {
		case <-cntrlldp.stop_chan:
			// Already closed
		default:
			close(cntrlldp.stop_chan)
		}
	}
}

func (t *TUI) setupUI() {
	t.pages = tview.NewPages()
	t.pages.SetBackgroundColor(tcell.ColorBlack)

	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Server Node[white]")
	t.statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			if !cntrlldp.should_stop.Swap(true) {
				select {
				case <-cntrlldp.stop_chan:
					// Already closed
				default:
					close(cntrlldp.stop_chan)
				}
			}
			t.app.Stop()
			return nil
		}
		return event
	})

	t.showMainScreen()
}

func (t *TUI) showMainScreen() {
	// Users tabela
	t.usersTable = tview.NewTable().
		SetBorders(true).
		SetSelectable(true, false)
	t.usersTable.SetBorder(true).SetTitle(" Users ")
	t.usersTable.SetBackgroundColor(tcell.ColorBlack)
	t.usersTable.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack))

	// Topics tabela
	t.topicsTable = tview.NewTable().
		SetBorders(true).
		SetSelectable(true, false)
	t.topicsTable.SetBorder(true).SetTitle(" Topics ")
	t.topicsTable.SetBackgroundColor(tcell.ColorBlack)
	t.topicsTable.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack))

	// Chain info
	t.chainInfo = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	t.chainInfo.SetBorder(true).SetTitle(" Chain Info ")
	t.chainInfo.SetBackgroundColor(tcell.ColorBlack)

	// Logi
	t.logsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	t.logsView.SetBorder(true).SetTitle(" Logs ")
	t.logsView.SetBackgroundColor(tcell.ColorBlack)

	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Keys:[white] Tab=switch panels | PgUp/PgDn=switch pages | q=quit")
	helpText.SetBackgroundColor(tcell.ColorDarkGreen)

	// Leva stran - Users in Topics
	leftFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.usersTable, 0, 1, true).
		AddItem(t.topicsTable, 0, 1, false)

	// Desna stran - Chain info in Logi
	rightFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.chainInfo, 8, 0, false).
		AddItem(t.logsView, 0, 1, false)

	// Glavna razporeditev
	mainFlex := tview.NewFlex().
		AddItem(leftFlex, 0, 1, true).
		AddItem(rightFlex, 0, 1, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true).
		AddItem(helpText, 1, 0, false).
		AddItem(t.statusBar, 1, 0, false)

	// Input capture za navigacijo
	focusables := []tview.Primitive{t.usersTable, t.topicsTable, t.chainInfo, t.logsView}
	currentFocus := 0

	t.pages.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			currentFocus = (currentFocus + 1) % len(focusables)
			t.setFocusPanel(focusables[currentFocus])
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				if !cntrlldp.should_stop.Swap(true) {
					select {
					case <-cntrlldp.stop_chan:
						// Already closed
					default:
						close(cntrlldp.stop_chan)
					}
				}
				t.app.Stop()
				return nil
			}
		}
		return event
	})

	t.pages.AddPage("main", layout, true, true)
	t.app.SetRoot(t.pages, true)
	t.setFocusPanel(t.usersTable)
}

func (t *TUI) setFocusPanel(p tview.Primitive) {
	t.usersTable.SetBorderColor(tcell.ColorWhite)
	t.topicsTable.SetBorderColor(tcell.ColorWhite)
	t.chainInfo.SetBorderColor(tcell.ColorWhite)
	t.logsView.SetBorderColor(tcell.ColorWhite)
	t.usersTable.SetBorderAttributes(0)
	t.topicsTable.SetBorderAttributes(0)
	t.chainInfo.SetBorderAttributes(0)
	t.logsView.SetBorderAttributes(0)

	if box, ok := p.(interface{ SetBorderColor(tcell.Color) *tview.Box }); ok {
		box.SetBorderColor(tcell.ColorWhite)
		if attrBox, ok := p.(interface {
			SetBorderAttributes(tcell.AttrMask) *tview.Box
		}); ok {
			attrBox.SetBorderAttributes(tcell.AttrBold)
		}
	}
	t.app.SetFocus(p)
}

func (t *TUI) updateStatusBar() {
	selfAddr := ""
	selfID := int64(-1)
	if cntrlldp.self != nil {
		selfAddr = cntrlldp.self.address
		selfID = cntrlldp.self.id
	}

	prevAddr := "nil"
	nextAddr := "nil"
	cntrlldp.mtx.RLock()
	if cntrlldp.chain_prev != nil {
		prevAddr = cntrlldp.chain_prev.address
	}
	if cntrlldp.chain_next != nil {
		nextAddr = cntrlldp.chain_next.address
	}
	cntrlldp.mtx.RUnlock()

	msrv.users_mtx.RLock()
	usersCount := len(msrv.users)
	msrv.users_mtx.RUnlock()

	msrv.topics_mtx.RLock()
	topicsCount := len(msrv.topics)
	msrv.topics_mtx.RUnlock()

	t.statusBar.SetText(fmt.Sprintf("[green]Node: %s (ID: %d)[white] | Prev: %s | Next: %s | Users: %d | Topics: %d",
		selfAddr, selfID, prevAddr, nextAddr, usersCount, topicsCount))
}

func (t *TUI) updateUsersTable() {
	t.usersTable.Clear()

	// Header
	t.usersTable.SetCell(0, 0, tview.NewTableCell("[yellow]ID[white]").SetSelectable(false))
	t.usersTable.SetCell(0, 1, tview.NewTableCell("[yellow]Name[white]").SetSelectable(false))

	msrv.users_mtx.RLock()
	defer msrv.users_mtx.RUnlock()

	for id, user := range msrv.users {
		t.usersTable.SetCellSimple(id+1, 0, strconv.Itoa(id))
		t.usersTable.SetCellSimple(id+1, 1, user.name)
	}
}

func (t *TUI) updateTopicsTable() {
	t.topicsTable.Clear()

	// Header
	t.topicsTable.SetCell(0, 0, tview.NewTableCell("[yellow]ID[white]").SetSelectable(false))
	t.topicsTable.SetCell(0, 1, tview.NewTableCell("[yellow]Name[white]").SetSelectable(false))
	t.topicsTable.SetCell(0, 2, tview.NewTableCell("[yellow]Messages[white]").SetSelectable(false))

	msrv.topics_mtx.RLock()
	defer msrv.topics_mtx.RUnlock()

	for id, topic := range msrv.topics {
		t.topicsTable.SetCellSimple(id+1, 0, strconv.Itoa(id))
		t.topicsTable.SetCellSimple(id+1, 1, topic.name)
		t.topicsTable.SetCellSimple(id+1, 2, strconv.Itoa(len(topic.messages)))
	}
}

func (t *TUI) updateChainInfo() {
	var sb strings.Builder

	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	sb.WriteString("[yellow]Self:[white] ")
	if cntrlldp.self != nil {
		sb.WriteString(fmt.Sprintf("ID=%d, Addr=%s\n", cntrlldp.self.id, cntrlldp.self.address))
	} else {
		sb.WriteString("nil\n")
	}

	sb.WriteString("[yellow]Prev (Head):[white] ")
	if cntrlldp.chain_prev != nil {
		sb.WriteString(fmt.Sprintf("ID=%d, Addr=%s\n", cntrlldp.chain_prev.id, cntrlldp.chain_prev.address))
	} else {
		sb.WriteString("[green]I am HEAD[white]\n")
	}

	sb.WriteString("[yellow]Next (Tail):[white] ")
	if cntrlldp.chain_next != nil {
		sb.WriteString(fmt.Sprintf("ID=%d, Addr=%s\n", cntrlldp.chain_next.id, cntrlldp.chain_next.address))
	} else {
		sb.WriteString("[green]I am TAIL[white]\n")
	}

	sb.WriteString("[yellow]Control Plane:[white] ")
	controlAddrs := make([]string, 0, len(cntrlldp.control))
	for addr := range cntrlldp.control {
		controlAddrs = append(controlAddrs, addr)
	}
	sb.WriteString(strings.Join(controlAddrs, ", "))

	t.chainInfo.SetText(sb.String())
}

func (t *TUI) addLog(msg string) {
	timestamp := time.Now().Format("15:04:05")
	logEntry := fmt.Sprintf("[dim]%s[white] %s", timestamp, msg)
	t.logs = append(t.logs, logEntry)

	// Ohrani samo zadnjih 100 logov
	if len(t.logs) > 100 {
		t.logs = t.logs[len(t.logs)-100:]
	}

	t.logsView.SetText(strings.Join(t.logs, "\n"))
	t.logsView.ScrollToEnd()
}

func (t *TUI) updater() {
	for {
		time.Sleep(time.Second)

		if cntrlldp.should_stop.Load() {
			return
		}

		t.app.QueueUpdateDraw(func() {
			t.updateStatusBar()
			t.updateUsersTable()
			t.updateTopicsTable()
			t.updateChainInfo()
		})
	}
}
