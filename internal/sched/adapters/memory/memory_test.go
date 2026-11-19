// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/sched/adapters/memory"
	"github.com/SDK-E/agentsone/internal/sched/domain"
	"github.com/SDK-E/agentsone/internal/sched/schedtest"
)

func TestTaskRepository(t *testing.T) {
	t.Parallel()
	schedtest.RunTaskRepositoryContract(t, func(*testing.T) domain.TaskRepository { return memory.NewTasks() })
}

func TestScheduleRepository(t *testing.T) {
	t.Parallel()
	schedtest.RunScheduleRepositoryContract(t, func(*testing.T) domain.ScheduleRepository { return memory.NewSchedules() })
}
