/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"errors"
	"os"
	"time"

	"github.com/github/copilot-sdk/go/rpc"
)

// SessionFSProvider is the interface that SDK users implement to provide
// a session filesystem. Methods use idiomatic Go error handling: return an
// error for failures (the adapter maps os.ErrNotExist → ENOENT automatically).
//
// To add SQLite support, also implement [SessionFSSqliteProvider] on the same type.
type SessionFSProvider interface {
	// ReadFile reads the full content of a file. Return os.ErrNotExist (or wrap it)
	// if the file does not exist.
	ReadFile(path string) (string, error)
	// WriteFile writes content to a file, creating it and parent directories if needed.
	// mode is an optional POSIX-style permission mode. Pass nil to use the OS default.
	WriteFile(path string, content string, mode *int) error
	// AppendFile appends content to a file, creating it and parent directories if needed.
	// mode is an optional POSIX-style permission mode. Pass nil to use the OS default.
	AppendFile(path string, content string, mode *int) error
	// Exists checks whether the given path exists.
	Exists(path string) (bool, error)
	// Stat returns metadata about a file or directory.
	// Return os.ErrNotExist if the path does not exist.
	Stat(path string) (*SessionFSFileInfo, error)
	// Mkdir creates a directory. If recursive is true, create parent directories as needed.
	// mode is an optional POSIX-style permission mode (e.g., 0o755). Pass nil to use the OS default.
	MakeDirectory(path string, recursive bool, mode *int) error
	// Readdir lists the names of entries in a directory.
	// Return os.ErrNotExist if the directory does not exist.
	ReadDirectory(path string) ([]string, error)
	// ReaddirWithTypes lists entries with type information.
	// Return os.ErrNotExist if the directory does not exist.
	ReadDirectoryWithTypes(path string) ([]rpc.SessionFSReaddirWithTypesEntry, error)
	// Rm removes a file or directory. If recursive is true, remove contents too.
	// If force is true, do not return an error when the path does not exist.
	Remove(path string, recursive bool, force bool) error
	// Rename moves/renames a file or directory.
	Rename(src string, dest string) error
}

// SessionFSSqliteProvider is an optional interface that a [SessionFSProvider]
// may also implement to support per-session SQLite databases. The adapter
// checks for this interface at runtime using a type assertion. If the
// provider does not implement it, SQLite requests return an "unsupported" error.
//
// Providers are already session-scoped (created per session by the factory),
// so these methods do not take a session ID parameter.
type SessionFSSqliteProvider interface {
	// SqliteQuery executes a SQLite query against the provider's per-session database.
	SqliteQuery(queryType rpc.SessionFSSqliteQueryType, query string, params map[string]any) (*SessionFSSqliteQueryResult, error)
	// SqliteExists checks whether the provider has a SQLite database for the session.
	SqliteExists() (bool, error)
}

// SessionFSSqliteTransactionProvider is an optional interface that a
// [SessionFSSqliteProvider] may also implement to support atomic transactions.
type SessionFSSqliteTransactionProvider interface {
	// SqliteTransaction executes statements atomically against the provider's
	// per-session database, applying busy handling to every statement and rolling
	// the whole batch back if any statement fails. It returns one result per
	// statement, in the same order.
	//
	// Return a [*SessionFSSqliteTransactionFailure] to classify the failure for
	// the runtime; any other error is reported as
	// [rpc.SessionFSSqliteTransactionErrorClassFatal].
	SqliteTransaction(statements []rpc.SessionFSSqliteTransactionStatement) ([]SessionFSSqliteQueryResult, error)
}

// SessionFSSqliteTransactionFailure classifies a SQLite transaction failure for
// the runtime. Return it from [SessionFSSqliteTransactionProvider.SqliteTransaction] with
// [rpc.SessionFSSqliteTransactionErrorClassBusyOrLocked] when SQLite reported
// BUSY or LOCKED before commit and the transaction was rolled back, so the
// runtime knows the call is safe to retry.
type SessionFSSqliteTransactionFailure struct {
	// Class is the failure classification reported to the runtime.
	Class rpc.SessionFSSqliteTransactionErrorClass
	// Message describes the failure.
	Message string
}

func (e *SessionFSSqliteTransactionFailure) Error() string {
	return e.Message
}

// SessionFSSqliteQueryResult holds the result of a SQLite query execution.
// Same shape as the generated RPC type but without the Error field,
// since providers signal errors by returning a Go error.
type SessionFSSqliteQueryResult struct {
	Columns         []string         `json:"columns"`
	Rows            []map[string]any `json:"rows"`
	RowsAffected    int64            `json:"rowsAffected"`
	LastInsertRowid *int64           `json:"lastInsertRowid,omitempty"`
}

// SessionFSFileInfo holds file metadata returned by SessionFSProvider.Stat.
type SessionFSFileInfo struct {
	IsFile      bool
	IsDirectory bool
	Size        int64
	Mtime       time.Time
	Birthtime   time.Time
}

// sessionFSAdapter wraps a SessionFSProvider to implement rpc.SessionFSHandler,
// converting idiomatic Go errors into SessionFSError results.
type sessionFSAdapter struct {
	provider SessionFSProvider
}

func newSessionFSAdapter(provider SessionFSProvider) rpc.SessionFSHandler {
	return &sessionFSAdapter{provider: provider}
}

func (a *sessionFSAdapter) ReadFile(request *rpc.SessionFSReadFileRequest) (*rpc.SessionFSReadFileResult, error) {
	content, err := a.provider.ReadFile(request.Path)
	if err != nil {
		return &rpc.SessionFSReadFileResult{Error: toSessionFSError(err)}, nil
	}
	return &rpc.SessionFSReadFileResult{Content: content}, nil
}

func (a *sessionFSAdapter) WriteFile(request *rpc.SessionFSWriteFileRequest) (*rpc.SessionFSError, error) {
	var mode *int
	if request.Mode != nil {
		m := int(*request.Mode)
		mode = &m
	}
	if err := a.provider.WriteFile(request.Path, request.Content, mode); err != nil {
		return toSessionFSError(err), nil
	}
	return nil, nil
}

func (a *sessionFSAdapter) AppendFile(request *rpc.SessionFSAppendFileRequest) (*rpc.SessionFSError, error) {
	var mode *int
	if request.Mode != nil {
		m := int(*request.Mode)
		mode = &m
	}
	if err := a.provider.AppendFile(request.Path, request.Content, mode); err != nil {
		return toSessionFSError(err), nil
	}
	return nil, nil
}

func (a *sessionFSAdapter) Exists(request *rpc.SessionFSExistsRequest) (*rpc.SessionFSExistsResult, error) {
	exists, err := a.provider.Exists(request.Path)
	if err != nil {
		return &rpc.SessionFSExistsResult{Exists: false}, nil
	}
	return &rpc.SessionFSExistsResult{Exists: exists}, nil
}

func (a *sessionFSAdapter) Stat(request *rpc.SessionFSStatRequest) (*rpc.SessionFSStatResult, error) {
	info, err := a.provider.Stat(request.Path)
	if err != nil {
		return &rpc.SessionFSStatResult{Error: toSessionFSError(err)}, nil
	}
	return &rpc.SessionFSStatResult{
		IsFile:      info.IsFile,
		IsDirectory: info.IsDirectory,
		Size:        info.Size,
		Mtime:       info.Mtime,
		Birthtime:   info.Birthtime,
	}, nil
}

func (a *sessionFSAdapter) Mkdir(request *rpc.SessionFSMkdirRequest) (*rpc.SessionFSError, error) {
	recursive := request.Recursive != nil && *request.Recursive
	var mode *int
	if request.Mode != nil {
		m := int(*request.Mode)
		mode = &m
	}
	if err := a.provider.MakeDirectory(request.Path, recursive, mode); err != nil {
		return toSessionFSError(err), nil
	}
	return nil, nil
}

func (a *sessionFSAdapter) Readdir(request *rpc.SessionFSReaddirRequest) (*rpc.SessionFSReaddirResult, error) {
	entries, err := a.provider.ReadDirectory(request.Path)
	if err != nil {
		return &rpc.SessionFSReaddirResult{Error: toSessionFSError(err)}, nil
	}
	return &rpc.SessionFSReaddirResult{Entries: entries}, nil
}

func (a *sessionFSAdapter) ReaddirWithTypes(request *rpc.SessionFSReaddirWithTypesRequest) (*rpc.SessionFSReaddirWithTypesResult, error) {
	entries, err := a.provider.ReadDirectoryWithTypes(request.Path)
	if err != nil {
		return &rpc.SessionFSReaddirWithTypesResult{Error: toSessionFSError(err)}, nil
	}
	return &rpc.SessionFSReaddirWithTypesResult{Entries: entries}, nil
}

func (a *sessionFSAdapter) Rm(request *rpc.SessionFSRmRequest) (*rpc.SessionFSError, error) {
	recursive := request.Recursive != nil && *request.Recursive
	force := request.Force != nil && *request.Force
	if err := a.provider.Remove(request.Path, recursive, force); err != nil {
		return toSessionFSError(err), nil
	}
	return nil, nil
}

func (a *sessionFSAdapter) Rename(request *rpc.SessionFSRenameRequest) (*rpc.SessionFSError, error) {
	if err := a.provider.Rename(request.Src, request.Dest); err != nil {
		return toSessionFSError(err), nil
	}
	return nil, nil
}

func (a *sessionFSAdapter) SqliteQuery(request *rpc.SessionFSSqliteQueryRequest) (*rpc.SessionFSSqliteQueryResult, error) {
	sp, ok := a.provider.(SessionFSSqliteProvider)
	if !ok {
		msg := "SQLite is not supported by this session filesystem provider"
		return &rpc.SessionFSSqliteQueryResult{
			Columns:      []string{},
			Rows:         []map[string]any{},
			RowsAffected: 0,
			Error:        &rpc.SessionFSError{Code: rpc.SessionFSErrorCodeUNKNOWN, Message: &msg},
		}, nil
	}
	result, err := sp.SqliteQuery(request.QueryType, request.Query, request.Params)
	if err != nil {
		return &rpc.SessionFSSqliteQueryResult{
			Columns:      []string{},
			Rows:         []map[string]any{},
			RowsAffected: 0,
			Error:        toSessionFSError(err),
		}, nil
	}
	if result == nil {
		return &rpc.SessionFSSqliteQueryResult{
			Columns:      []string{},
			Rows:         []map[string]any{},
			RowsAffected: 0,
		}, nil
	}
	wireResult := toWireSqliteQueryResult(*result)
	return &wireResult, nil
}

func (a *sessionFSAdapter) SqliteTransaction(request *rpc.SessionFSSqliteTransactionRequest) (*rpc.SessionFSSqliteTransactionResult, error) {
	sp, ok := a.provider.(SessionFSSqliteTransactionProvider)
	if !ok {
		return &rpc.SessionFSSqliteTransactionResult{
			Results: []rpc.SessionFSSqliteQueryResult{},
			Error: &rpc.SessionFSSqliteTransactionError{
				ErrorClass: rpc.SessionFSSqliteTransactionErrorClassFatal,
				Message:    "SQLite is not supported by this session filesystem provider",
			},
		}, nil
	}
	results, err := sp.SqliteTransaction(request.Statements)
	if err != nil {
		return &rpc.SessionFSSqliteTransactionResult{
			Results: []rpc.SessionFSSqliteQueryResult{},
			Error:   toSessionFSSqliteTransactionError(err),
		}, nil
	}
	wireResults := make([]rpc.SessionFSSqliteQueryResult, 0, len(results))
	for _, result := range results {
		wireResults = append(wireResults, toWireSqliteQueryResult(result))
	}
	return &rpc.SessionFSSqliteTransactionResult{Results: wireResults}, nil
}

func toWireSqliteQueryResult(result SessionFSSqliteQueryResult) rpc.SessionFSSqliteQueryResult {
	columns := result.Columns
	if columns == nil {
		columns = []string{}
	}
	rows := result.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	return rpc.SessionFSSqliteQueryResult{
		Columns:         columns,
		Rows:            rows,
		RowsAffected:    result.RowsAffected,
		LastInsertRowid: result.LastInsertRowid,
	}
}

func (a *sessionFSAdapter) SqliteExists(request *rpc.SessionFSSqliteExistsRequest) (*rpc.SessionFSSqliteExistsResult, error) {
	sp, ok := a.provider.(SessionFSSqliteProvider)
	if !ok {
		return &rpc.SessionFSSqliteExistsResult{Exists: false}, nil
	}
	exists, err := sp.SqliteExists()
	if err != nil {
		return &rpc.SessionFSSqliteExistsResult{Exists: false}, nil
	}
	return &rpc.SessionFSSqliteExistsResult{Exists: exists}, nil
}

func toSessionFSError(err error) *rpc.SessionFSError {
	code := rpc.SessionFSErrorCodeUNKNOWN
	if errors.Is(err, os.ErrNotExist) {
		code = rpc.SessionFSErrorCodeENOENT
	}
	msg := err.Error()
	return &rpc.SessionFSError{Code: code, Message: &msg}
}

func toSessionFSSqliteTransactionError(err error) *rpc.SessionFSSqliteTransactionError {
	var failure *SessionFSSqliteTransactionFailure
	if errors.As(err, &failure) {
		return &rpc.SessionFSSqliteTransactionError{
			ErrorClass: failure.Class,
			Message:    failure.Message,
		}
	}
	return &rpc.SessionFSSqliteTransactionError{
		ErrorClass: rpc.SessionFSSqliteTransactionErrorClassFatal,
		Message:    err.Error(),
	}
}
