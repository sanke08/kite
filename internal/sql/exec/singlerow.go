package exec

// SingleRow produces exactly one empty row, then stops.
//
// WHY it exists: SELECT 1+2 has no table, but Project still has to run its
// expressions once. Rather than Project growing a "no child" special case,
// the plan gets a source that yields one row with no columns in it — so
// there is exactly one operator shape, always.
//
// EG: SELECT 1+2 plans as
//
//	Project{[1+2]}
//	  SingleRow      -> Row{} once, then nil
type SingleRow struct {
	yielded bool
}

func NewSingleRow() *SingleRow {
	return &SingleRow{}
}

func (single *SingleRow) Open() error {
	single.yielded = false
	return nil
}

func (single *SingleRow) Next() (Row, error) {
	if single.yielded {
		return nil, nil
	}
	single.yielded = true
	return Row{}, nil
}

func (single *SingleRow) Close() error {
	return nil
}

func (single *SingleRow) Columns() []string {
	return nil
}
