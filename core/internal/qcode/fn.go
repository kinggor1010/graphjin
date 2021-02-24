package qcode

import (
	"fmt"
	"github.com/dosco/graphjin/core/internal/sdata"
	"strings"
)

func (co *Compiler) isFunction(sel *Select, fname string) (Function, string, bool, error) {
	var fnExp string
	var agg bool
	var err error

	fn := Function{FieldName: fname}

	switch {
	case fname == "search_rank":
		fn.Name = "search_rank"

		if _, ok := sel.ArgMap["search"]; !ok {
			return fn, "", false, fmt.Errorf("no search defined: %s", fname)
		}

	case strings.HasPrefix(fname, "search_headline_"):
		fn.Name = "search_headline"
		fnExp = fname[16:]

		if _, ok := sel.ArgMap["search"]; !ok {
			return fn, "", false, fmt.Errorf("no search defined: %s", fname)
		}

	case fname == "__typename":
		sel.Typename = true
		fn.skip = true

	case strings.HasSuffix(fname, "_cursor"):
		fn.skip = true

	default:
		n := co.funcPrefixLen(fname)
		if n != 0 {
			fnExp = fname[n:]
			fn.Name = fname[:(n - 1)]
			agg = true
		}
	}

	return fn, fnExp, agg, err
}

func (co *Compiler) funcPrefixLen(col string) int {
	switch {
	case strings.HasPrefix(col, "avg_"):
		return 4
	case strings.HasPrefix(col, "count_"):
		return 6
	case strings.HasPrefix(col, "max_"):
		return 4
	case strings.HasPrefix(col, "min_"):
		return 4
	case strings.HasPrefix(col, "sum_"):
		return 4
	case strings.HasPrefix(col, "stddev_"):
		return 7
	case strings.HasPrefix(col, "stddev_pop_"):
		return 11
	case strings.HasPrefix(col, "stddev_samp_"):
		return 12
	case strings.HasPrefix(col, "variance_"):
		return 9
	case strings.HasPrefix(col, "var_pop_"):
		return 8
	case strings.HasPrefix(col, "var_samp_"):
		return 9
	}
	fnLen := len(col)

	for k := range co.s.GetFunctions() {
		kLen := len(k)
		if kLen < fnLen && k[0] == col[0] && strings.HasPrefix(col, k) && col[kLen] == '_' {
			return kLen + 1
		}
	}

	return 0
}

func (co *Compiler) parseFuncExpression(sel *Select, fn *Function, fnExp string) error {
	var err error

	if strings.HasPrefix(fnExp, "_") {
		parts := strings.Split(fnExp[1:], "__")
		var column sdata.DBColumn
		table, err := co.s.Find(co.c.DBSchema, parts[0])
		if err != nil {
			return err
		}
		if len(parts) == 1 {
			column = table.PrimaryCol
		} else {
			col, err := table.GetColumn(parts[1])
			if err != nil {
				return err
			}
			column = col
		}

		fnSel := &Select{
			ParentID: sel.ID,
			Table:    table.Name,
		}
		var paths []sdata.TPath
		paths, err = co.s.FindPath(table.Name, sel.Table)
		fnSel.addCol(Column{
			Col: column,
		}, true)
		fnSel.Rel = sdata.PathToRel(paths[0])
		for _, p := range paths[1:] {
			fnSel.Joins = append(fnSel.Joins, sdata.PathToRel(p))
		}
		fn.Sel = fnSel
	} else {
		fn.Col, err = sel.Ti.GetColumn(fnExp)
	}

	return err
}
