package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/tablespace/pkg/client"
	keys "github.com/kontrolplane/tablespace/pkg/keys"
	"github.com/kontrolplane/tablespace/pkg/tablespace"
	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

// Layout constants for consistent sizing across all views
const (
	attributesHeight = 6   // Attributes plus spacing in the details view
	tabBarHeight     = 2   // Tab bar plus spacing above the details table
	tableHeaderRows  = 2   // Column titles and the rule under them
	minTableHeight   = 5   // Minimum body rows for any table
	minContentWidth  = 100 // Below this, or minContentHeight, a notice asks for a larger terminal
	minContentHeight = 16
	chromeWidth      = 2 // Frame borders
	chromeHeight     = 8 // Header, spacing, frame edges and padding, footer
)

// The content area fills the terminal. setLayout resizes it and everything derived from it.
var (
	contentWidth  = 140
	contentHeight = 25
	frameWidth    = contentWidth + chromeWidth
)

func setLayout(width, height int) {
	contentWidth = max(minContentWidth, width-chromeWidth)
	contentHeight = max(minContentHeight, height-chromeHeight)
	frameWidth = contentWidth + chromeWidth
	setPanelLayout()
}

// resize fits the tables, inputs and forms of every page to the current layout.
func (m model) resize() model {
	o := &m.state.databaseOverview
	for _, t := range []*dataTable{&o.databasesTable, &o.backendsTable} {
		t.setSize(contentWidth-4, m.overviewTableHeight())
	}

	d := &m.state.databaseDetails
	for _, t := range []*dataTable{&d.tablesTable, &d.indexesTable} {
		t.setSize(contentWidth-4, m.overviewTableHeight())
	}

	td := &m.state.tableDetails
	for _, t := range []*dataTable{&td.columnsTable, &td.indexesTable, &td.constraintsTable} {
		t.setSize(contentWidth-4, m.getDetailsTableHeight())
	}
	m = m.updateRowsTable()
	if td.attributes != "" {
		td.attributes = renderTableAttributes(td.table, td.paging, td.schemaLoaded)
	}

	m = m.resizeRowDetails().resizeEditor()

	switch m.page {
	case databaseCreate:
		if f := m.state.databaseCreate.form; f != nil {
			f.WithWidth(formWidth()).WithHeight(formHeight())
		}
	case databaseDrop:
		c := &m.state.databaseDrop.confirm
		c.input.SetWidth(typedConfirmWidth(c.want))
	case tableDrop:
		c := &m.state.tableDrop.confirm
		c.input.SetWidth(typedConfirmWidth(c.want))
	}
	return m
}

type model struct {
	projectName string
	programName string
	page        page
	state       state
	pool        *pgxpool.Pool
	config      *pgxpool.Config     // what pool was opened with, other databases are opened from it
	session     *tablespace.Session // the editor's connection
	info        client.Info
	server      tablespace.Server
	serverOK    bool
	online      bool // the last round trip to the server succeeded
	context     context.Context
	width       int
	height      int
	keys        keys.KeyMap
	showHelp    bool
	error       string
	loading     bool
	loadingMsg  string
	spinner     spinner.Model
	spinning    bool
	statusMsg   string
	statusTone  styles.Tone
	statusGen   int
	refreshGen  int
	busy        bool   // a create, drop, delete or signal is in flight
	armed       bool   // esc was pressed once on a form with input, the next esc discards it
	rowsGen     uint64 // last row load issued
	rowsApplied uint64 // last row load shown
	paused      bool   // the refresh ticks are stopped
	refreshedAt time.Time
	clocking    bool // a tick for the time since the last refresh is pending
}

// overviewTableHeight returns the body rows of a table below a tab bar.
func (m model) overviewTableHeight() int {
	return max(minTableHeight, contentHeight-tabBarHeight-tableHeaderRows)
}

// getDetailsTableHeight returns the body rows of the tables in the details view.
func (m model) getDetailsTableHeight() int {
	available := contentHeight - attributesHeight - tabBarHeight - tableHeaderRows
	if available < minTableHeight {
		return minTableHeight
	}
	return available
}

type state struct {
	databaseOverview databaseOverviewState
	databaseCreate   databaseCreateState
	databaseDrop     databaseDropState
	backendDetails   backendDetailsState
	backendSignal    backendSignalState
	databaseDetails  databaseDetailsState
	tableDetails     tableDetailsState
	tableDrop        tableDropState
	rowDetails       rowDetailsState
	rowDelete        rowDeleteState
	sqlEditor        sqlEditorState
}
