package code

import (
	"context"
	"errors"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestRecordFailureFull(t *testing.T) {
	requireDB(t)
	recordFailure(context.Background(), "CODE-RF", "CODE-1", "https://r", "title", "stage1", errors.New("boom"))
	got, _ := db.GetCodeTask(context.Background(), "CODE-RF")
	if got == nil || got.Status != data.CodeFailed || got.LastErrorStage != "stage1" {
		t.Errorf("got = %+v", got)
	}
}
