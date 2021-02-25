//go:generate stringer -type=RelType -output=./gen_string.go

package sdata

import (
	"fmt"
	"strings"

	"gonum.org/v1/gonum/graph/multi"
)

type edgeInfo struct {
	nodeID  int64
	edgeIDs []int64
}

type nodeInfo struct {
	nodeID int64
}

type DBSchema struct {
	typ    string                       // db type
	ver    int                          // db version
	schema string                       // db schema
	name   string                       // db name
	tables []DBTable                    // tables
	vt     map[string]VirtualTable      // for polymorphic relationships
	fm     map[string]DBFunction        // db functions
	tindex map[string]nodeInfo          // table index
	ai     map[string]nodeInfo          // table alias index
	re     map[int64]TEdge              // recursive edges
	ae     map[int64]TEdge              // all other edges
	ei     map[string][]edgeInfo        // edges index
	rg     *multi.WeightedDirectedGraph // relationship graph
}

type RelType int

const (
	RelNone RelType = iota
	RelOneToOne
	RelOneToMany
	RelPolymorphic
	RelRecursive
	RelEmbedded
	RelRemote
	RelSkip
)

type DBRelThrough struct {
	Ti   DBTable
	ColL DBColumn
	ColR DBColumn
}

type DBRelLeft struct {
	Ti  DBTable
	Col DBColumn
}

type DBRelRight struct {
	VTable string
	Ti     DBTable
	Col    DBColumn
}

type DBRel struct {
	Type    RelType
	Through DBRelThrough
	Left    DBRelLeft
	Right   DBRelRight
}

func NewDBSchema(
	info *DBInfo,
	aliases map[string][]string) (*DBSchema, error) {

	schema := &DBSchema{
		typ:    info.Type,
		ver:    info.Version,
		schema: info.Schema,
		name:   info.Name,
		vt:     make(map[string]VirtualTable),
		tindex: make(map[string]nodeInfo),
		ai:     make(map[string]nodeInfo),
		re:     make(map[int64]TEdge),
		ae:     make(map[int64]TEdge),
		ei:     make(map[string][]edgeInfo),
		rg:     multi.NewWeightedDirectedGraph(),
	}

	// schema.rg.EdgeWeightFunc = func(e graph.WeightedLines) float64 {
	// 	var min float64 = 10
	// 	for e.Next() {
	// 		l := e.WeightedLine()
	// 		if l.Weight() < min {
	// 			min = l.Weight()
	// 		}
	// 	}
	// 	return min
	// }

	var nids []int64

	for _, t := range info.Tables {
		nids = append(nids, schema.addNode(t))
	}

	for _, nid := range nids {
		t := schema.tables[nid]
		schema.addAliases(t, nid, aliases[strings.ToLower(t.Name)])
	}

	for _, t := range info.VTables {
		if err := schema.addVirtual(t); err != nil {
			return nil, err
		}
	}

	for _, t := range schema.tables {
		err := schema.addRels(t)
		if err != nil {
			return nil, err
		}
	}

	for k, f := range info.Functions {
		if len(f.Params) == 1 {
			schema.fm[strings.ToLower(f.Name)] = info.Functions[k]
		}
	}

	return schema, nil
}

func (s *DBSchema) addRels(t DBTable) error {
	var err error
	switch t.Type {
	case "json", "jsonb":
		err = s.addJsonRel(t)
	case "virtual":
		err = s.addPolymorphicRel(t)
	case "remote":
		err = s.addRemoteRel(t)
	}

	if err != nil {
		return err
	}

	return s.addColumnRels(t)
}

func (s *DBSchema) addJsonRel(t DBTable) error {
	st, err := s.Find(t.SecondaryCol.Schema, t.SecondaryCol.Table)
	if err != nil {
		return err
	}

	sc, err := st.GetColumn(t.SecondaryCol.Name)
	if err != nil {
		return err
	}

	return s.addToGraph(t, t.PrimaryCol, st, sc, RelEmbedded)
}

func (s *DBSchema) addPolymorphicRel(t DBTable) error {
	pt, err := s.Find(t.PrimaryCol.FKeySchema, t.PrimaryCol.FKeyTable)
	if err != nil {
		return err
	}

	pc, err := pt.GetColumn(t.PrimaryCol.FKeyCol)
	if err != nil {
		return err
	}

	return s.addToGraph(t, t.PrimaryCol, pt, pc, RelPolymorphic)
}

func (s *DBSchema) addRemoteRel(t DBTable) error {
	pt, err := s.Find(t.PrimaryCol.FKeySchema, t.PrimaryCol.FKeyTable)
	if err != nil {
		return err
	}

	pc, err := pt.GetColumn(t.PrimaryCol.FKeyCol)
	if err != nil {
		return err
	}

	return s.addToGraph(t, t.PrimaryCol, pt, pc, RelRemote)
}

func (s *DBSchema) addColumnRels(t DBTable) error {
	var err error

	for i := range t.Columns {
		c := t.Columns[i]

		if c.FKeyTable == "" {
			continue
		}

		if c.FKeySchema == "" {
			c.FKeySchema = t.Schema
		}

		v, ok := s.tindex[(c.FKeySchema + ":" + c.FKeyTable)]
		if !ok {
			return fmt.Errorf("foreign key table not found: %s.%s", c.FKeySchema, c.FKeyTable)
		}
		ft := s.tables[v.nodeID]

		if c.FKeyCol == "" {
			continue
		}

		fc, ok := ft.getColumn(c.FKeyCol)
		if !ok {
			return fmt.Errorf("foreign key column not found: %s.%s", c.FKeyTable, c.FKeyCol)
		}

		var rt RelType

		switch {
		case t.Name == c.FKeyTable:
			rt = RelRecursive
		case fc.UniqueKey:
			rt = RelOneToOne
		default:
			rt = RelOneToMany
		}

		if err = s.addToGraph(t, c, ft, fc, rt); err != nil {
			return err
		}
	}
	return nil
}

func (s *DBSchema) addVirtual(vt VirtualTable) error {
	s.vt[vt.Name] = vt

	for _, t := range s.tables {
		idCol, ok := t.getColumn(vt.IDColumn)
		if !ok {
			continue
		}

		typeCol, ok := t.getColumn(vt.TypeColumn)
		if !ok {
			continue
		}

		col1 := DBColumn{
			ID:         -1,
			Schema:     t.Schema,
			Table:      t.Name,
			Name:       vt.FKeyColumn,
			Type:       idCol.Type,
			FKeySchema: typeCol.Schema,
			FKeyTable:  typeCol.Table,
			FKeyCol:    typeCol.Name,
		}

		pt := DBTable{
			Name:       vt.Name,
			Schema:     t.Schema,
			Type:       "virtual",
			PrimaryCol: col1,
		}
		s.addNode(pt)
	}

	return nil
}

func (s *DBSchema) GetTables() []DBTable {
	return s.tables
}

func (ti *DBTable) getColumn(name string) (DBColumn, bool) {
	var c DBColumn
	if i, ok := ti.colMap[name]; ok {
		return ti.Columns[i], true
	}
	return c, false
}

func (ti *DBTable) GetColumn(name string) (DBColumn, error) {
	c, ok := ti.getColumn(name)
	if ok {
		return c, nil
	}
	return c, fmt.Errorf("column: '%s.%s' not found", ti.Name, name)
}

func (s *DBSchema) GetFunctions() map[string]DBFunction {
	return s.fm
}

func getRelName(colName string) string {
	cn := strings.ToLower(colName)

	if strings.HasSuffix(cn, "_id") {
		return colName[:len(colName)-3]
	}

	if strings.HasSuffix(cn, "_ids") {
		return colName[:len(colName)-4]
	}

	if strings.HasPrefix(cn, "id_") {
		return colName[3:]
	}

	if strings.HasPrefix(cn, "ids_") {
		return colName[4:]
	}

	return cn
}

func (s *DBSchema) DBType() string {
	return s.typ
}

func (s *DBSchema) DBVersion() int {
	return s.ver
}

func (s *DBSchema) DBSchema() string {
	return s.schema
}

func (s *DBSchema) DBName() string {
	return s.name
}
