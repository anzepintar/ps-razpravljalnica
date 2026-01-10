package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hashicorp/raft"
	"github.com/rivo/tview"
)

type TUI struct {
	app   *tview.Application
	pages *tview.Pages

	// Elementi prikaza
	statusBar  *tview.TextView
	nodesTable *tview.Table
	raftInfo   *tview.TextView
	logsView   *tview.TextView

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
}

func (t *TUI) setupUI() {
	t.pages = tview.NewPages()
	t.pages.SetBackgroundColor(tcell.ColorBlack)

	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Control Plane Node[white]")
	t.statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			t.app.Stop()
			return nil
		}
		return event
	})

	t.showMainScreen()
}

func (t *TUI) showMainScreen() {
	// Nodes tabela
	t.nodesTable = tview.NewTable().
		SetBorders(true).
		SetSelectable(true, false)
	t.nodesTable.SetBorder(true).SetTitle(" Data Plane Nodes (Chain) ")
	t.nodesTable.SetBackgroundColor(tcell.ColorBlack)

	// Raft info
	t.raftInfo = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	t.raftInfo.SetBorder(true).SetTitle(" Raft Cluster Info ")
	t.raftInfo.SetBackgroundColor(tcell.ColorBlack)

	// Logi
	t.logsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	t.logsView.SetBorder(true).SetTitle(" Logs ")
	t.logsView.SetBackgroundColor(tcell.ColorBlack)

	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Keys:[white] Tab=switch panels | q=quit")
	helpText.SetBackgroundColor(tcell.ColorDarkGreen)

	// Leva stran - Nodes
	leftFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.nodesTable, 0, 1, true)

	// Desna stran - Raft info in Logi
	rightFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.raftInfo, 12, 0, false).
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
	focusables := []tview.Primitive{t.nodesTable, t.raftInfo, t.logsView}
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
				t.app.Stop()
				return nil
			}
		}
		return event
	})

	t.pages.AddPage("main", layout, true, true)
	t.app.SetRoot(t.pages, true)
	t.setFocusPanel(t.nodesTable)
}

func (t *TUI) setFocusPanel(p tview.Primitive) {
	t.nodesTable.SetBorderColor(tcell.ColorWhite)
	t.raftInfo.SetBorderColor(tcell.ColorWhite)
	t.logsView.SetBorderColor(tcell.ColorWhite)

	if box, ok := p.(interface{ SetBorderColor(tcell.Color) *tview.Box }); ok {
		box.SetBorderColor(tcell.ColorOrange)
	}
	t.app.SetFocus(p)
}

func (t *TUI) updateStatusBar() {
	cntsrv.mtx.RLock()
	nodesCount := len(cntsrv.Nodes)
	idx := cntsrv.Idx
	cntsrv.mtx.RUnlock()

	isLeader := "no"
	if cntsrv.raft != nil && cntsrv.raft.State() == raft.Leader {
		isLeader = "[green]yes[white]"
	}

	headAddr := "none"
	tailAddr := "none"
	cntsrv.mtx.RLock()
	if len(cntsrv.Nodes) > 0 {
		headAddr = cntsrv.Nodes[0].address
		tailAddr = cntsrv.Nodes[len(cntsrv.Nodes)-1].address
	}
	cntsrv.mtx.RUnlock()

	t.statusBar.SetText(fmt.Sprintf("[green]Control Plane[white] | Nodes: %d | Idx: %d | Leader: %s | Head: %s | Tail: %s",
		nodesCount, idx, isLeader, headAddr, tailAddr))
}

func (t *TUI) updateNodesTable() {
	t.nodesTable.Clear()

	// Header
	t.nodesTable.SetCell(0, 0, tview.NewTableCell("[yellow]Position[white]").SetSelectable(false))
	t.nodesTable.SetCell(0, 1, tview.NewTableCell("[yellow]Node ID[white]").SetSelectable(false))
	t.nodesTable.SetCell(0, 2, tview.NewTableCell("[yellow]Address[white]").SetSelectable(false))
	t.nodesTable.SetCell(0, 3, tview.NewTableCell("[yellow]Role[white]").SetSelectable(false))

	cntsrv.mtx.RLock()
	defer cntsrv.mtx.RUnlock()

	for i, node := range cntsrv.Nodes {
		role := "middle"
		if i == 0 && len(cntsrv.Nodes) == 1 {
			role = "[green]HEAD+TAIL[white]"
		} else if i == 0 {
			role = "[green]HEAD[white]"
		} else if i == len(cntsrv.Nodes)-1 {
			role = "[blue]TAIL[white]"
		}

		t.nodesTable.SetCellSimple(i+1, 0, strconv.Itoa(i))
		t.nodesTable.SetCellSimple(i+1, 1, strconv.FormatInt(node.id, 10))
		t.nodesTable.SetCellSimple(i+1, 2, node.address)
		t.nodesTable.SetCell(i+1, 3, tview.NewTableCell(role))
	}
}

func (t *TUI) updateRaftInfo() {
	var sb strings.Builder

	if cntsrv.raft == nil {
		sb.WriteString("[red]Raft not initialized[white]\n")
		t.raftInfo.SetText(sb.String())
		return
	}

	state := cntsrv.raft.State()
	stateColor := "yellow"
	switch state {
	case raft.Leader:
		stateColor = "green"
	case raft.Follower:
		stateColor = "blue"
	case raft.Candidate:
		stateColor = "orange"
	}

	sb.WriteString(fmt.Sprintf("[yellow]State:[white] [%s]%s[white]\n", stateColor, state.String()))

	leaderAddr, leaderID := cntsrv.raft.LeaderWithID()
	sb.WriteString(fmt.Sprintf("[yellow]Leader:[white] %s (ID: %s)\n", leaderAddr, leaderID))

	stats := cntsrv.raft.Stats()
	sb.WriteString(fmt.Sprintf("[yellow]Term:[white] %s\n", stats["term"]))
	sb.WriteString(fmt.Sprintf("[yellow]Last Log Index:[white] %s\n", stats["last_log_index"]))
	sb.WriteString(fmt.Sprintf("[yellow]Commit Index:[white] %s\n", stats["commit_index"]))
	sb.WriteString(fmt.Sprintf("[yellow]Applied Index:[white] %s\n", stats["applied_index"]))

	sb.WriteString("\n[yellow]Raft Servers:[white]\n")
	cf := cntsrv.raft.GetConfiguration()
	if cf.Error() == nil {
		for _, server := range cf.Configuration().Servers {
			suffrage := "Voter"
			if server.Suffrage != raft.Voter {
				suffrage = "NonVoter"
			}
			sb.WriteString(fmt.Sprintf("  - %s (%s) [%s]\n", server.Address, server.ID, suffrage))
		}
	}

	t.raftInfo.SetText(sb.String())
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

		t.app.QueueUpdateDraw(func() {
			t.updateStatusBar()
			t.updateNodesTable()
			t.updateRaftInfo()
		})
	}
}
