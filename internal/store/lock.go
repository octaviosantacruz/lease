package store

import "context"

// LockExecutor serializes provider operations across CLI processes and the daemon.
// Una transacción SQLite separada conserva el lock hasta Close o salida abrupta;
// no depende de borrar un archivo de PID que pueda quedar obsoleto tras un crash.
func LockExecutor(ctx context.Context, path string) (func(), error) {
	db, err := connect(ctx, path+".executor.db", true)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, err
	}
	return func() { tx.Rollback(); db.Close() }, nil
}
