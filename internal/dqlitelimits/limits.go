//go:build keel_dqlite && cgo

// Package dqlitelimits installs bounded native query execution on dqlite's
// SQLite connections. No network-only timeout can interrupt a busy engine VM.
package dqlitelimits

/*
#cgo LDFLAGS: -lsqlite3 ${SRCDIR}/../../.keel/native/lib/libdqlite.a
#include <sqlite3.h>
#include <stdlib.h>
#include <time.h>
typedef struct {sqlite3 *db; void *statement; int remaining; struct timespec deadline;} keel_budget;
static int keel_progress(void *p) {
  keel_budget *b=p;
  struct timespec now;
  clock_gettime(CLOCK_MONOTONIC,&now);
  return --b->remaining<=0 || now.tv_sec>b->deadline.tv_sec ||
    (now.tv_sec==b->deadline.tv_sec && now.tv_nsec>=b->deadline.tv_nsec);
}
static int keel_trace(unsigned event, void *p, void *stmt, void *sql) {
  keel_budget *b=p;
  if(event==SQLITE_TRACE_PROFILE){if(b->statement==stmt)b->statement=NULL;return 0;}
  // Trigger subprograms report the same outer statement: share its budget.
  if(b->statement==stmt)return 0;
  b->statement=stmt;
  b->remaining=50000;
  clock_gettime(CLOCK_MONOTONIC,&b->deadline);
  b->deadline.tv_sec+=3;
  sqlite3_progress_handler(b->db,1000,keel_progress,b);
  return 0;
}
static void keel_lifetime(sqlite3_context *ctx,int argc,sqlite3_value **argv){sqlite3_result_null(ctx);}
static int keel_extension(sqlite3 *db,char **error,const sqlite3_api_routines *api){
  keel_budget *budget=calloc(1,sizeof(keel_budget));
  if(!budget)return SQLITE_NOMEM;
  budget->db=db;
  int rc=sqlite3_create_function_v2(db,"keel_internal_limits",0,SQLITE_UTF8,budget,keel_lifetime,NULL,NULL,free);
  if(rc!=SQLITE_OK)return rc;
  sqlite3_limit(db,SQLITE_LIMIT_LENGTH,1024*1024);
  sqlite3_limit(db,SQLITE_LIMIT_COLUMN,256);
  sqlite3_limit(db,SQLITE_LIMIT_VARIABLE_NUMBER,256);
  sqlite3_trace_v2(db,SQLITE_TRACE_STMT|SQLITE_TRACE_PROFILE,keel_trace,budget);
  sqlite3_progress_handler(db,1000,keel_progress,budget);
  sqlite3_busy_timeout(db,2000);
  return sqlite3_exec(db,"PRAGMA foreign_keys=ON",NULL,NULL,error);
}
static int keel_install(){return sqlite3_auto_extension((void(*)(void))keel_extension);}
*/
import "C"

import (
	"fmt"
	"sync"
)

var once sync.Once
var installError error

func Install() error {
	once.Do(func() {
		if rc := C.keel_install(); rc != 0 {
			installError = fmt.Errorf("install native SQL limits: %d", int(rc))
		}
	})
	return installError
}
