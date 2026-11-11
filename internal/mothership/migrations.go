package mothership

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// registerGoMigrations adds the Go migration the edge needs on top of the
// original JS ones.
//
// The native process registry used to live in a hand-rolled SQLite table. Now
// that PocketBase owns the database, the registry becomes a PocketBase
// collection so it is transactional with the rest of the control-plane data and
// is visible to the original hooks (for example the instance list).
func registerGoMigrations() {
	core.AppMigrations.Register(
		func(txApp core.App) error {
			col := core.NewBaseCollection("processes")
			col.System = true

			col.Fields.Add(
				&core.TextField{Name: "instance", Required: true, Max: 15},
				&core.NumberField{Name: "pid", OnlyInt: true},
				&core.NumberField{Name: "port", OnlyInt: true},
				&core.TextField{Name: "version"},
				&core.TextField{Name: "bin_path"},
				&core.TextField{Name: "workdir"},
				&core.TextField{Name: "status"},
				&core.DateField{Name: "started_at"},
				&core.DateField{Name: "stopped_at"},
				&core.NumberField{Name: "exit_code", OnlyInt: true},
				&core.NumberField{Name: "restarts", OnlyInt: true},
				&core.TextField{Name: "last_error"},
			)

			col.AddIndex("idx_processes_instance", true, "instance", "")

			// The registry is operator-only: no client may read or write it.
			col.ListRule = types.Pointer("")
			col.ViewRule = types.Pointer("")
			col.CreateRule = types.Pointer("")
			col.UpdateRule = types.Pointer("")
			col.DeleteRule = types.Pointer("")

			return txApp.Save(col)
		},
		func(txApp core.App) error {
			col, err := txApp.FindCollectionByNameOrId("processes")
			if err != nil {
				return nil
			}
			return txApp.Delete(col)
		},
		"1800000000_created_processes.go",
	)
}
