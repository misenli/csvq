package query

import (
	"context"
	"math"
	"sync"

	"github.com/mithrandie/csvq/lib/cmd"
)

var (
	gm    *GoroutineManager
	getGm sync.Once
)

const MinimumRequiredPerCPUCore = 80

func GetGoroutineManager() *GoroutineManager {
	getGm.Do(func() {
		gm = &GoroutineManager{
			Count:                  0,
			CountMutex:             new(sync.Mutex),
			MinimumRequiredPerCore: MinimumRequiredPerCPUCore,
		}
	})
	return gm
}

type GoroutineManager struct {
	Count                  int
	CountMutex             *sync.Mutex
	MinimumRequiredPerCore int
}

func (m *GoroutineManager) AssignRoutineNumber(recordLen int, minimumRequiredPerCore int) int {
	var greaterThanZero = func(i int) int {
		if i < 1 {
			return 1
		}
		return i
	}
	var min = func(i1 int, i2 int) int {
		if i1 < i2 {
			return i1
		}
		return i2
	}

	number := cmd.GetFlags().CPU
	if minimumRequiredPerCore < 1 {
		minimumRequiredPerCore = m.MinimumRequiredPerCore
	}

	number = min(number, greaterThanZero(int(math.Floor(float64(recordLen)/float64(minimumRequiredPerCore)))))

	m.CountMutex.Lock()
	defer m.CountMutex.Unlock()

	number = min(number, greaterThanZero(number-m.Count))

	m.Count += number - 1
	return number
}

func (m *GoroutineManager) Release() {
	m.CountMutex.Lock()
	if 0 < m.Count {
		m.Count--
	}
	m.CountMutex.Unlock()
}

type GoroutineTaskManager struct {
	Number int

	grCountMutex sync.Mutex
	grCount      int
	recordLen    int
	waitGroup    sync.WaitGroup
	err          error
}

func NewGoroutineTaskManager(recordLen int, minimumRequiredPerCore int) *GoroutineTaskManager {
	number := GetGoroutineManager().AssignRoutineNumber(recordLen, minimumRequiredPerCore)

	return &GoroutineTaskManager{
		Number:    number,
		grCount:   number - 1,
		recordLen: recordLen,
	}
}

func (m *GoroutineTaskManager) HasError() bool {
	return m.err != nil
}

func (m *GoroutineTaskManager) SetError(e error) {
	m.err = e
}

func (m *GoroutineTaskManager) Err() error {
	return m.err
}

func (m *GoroutineTaskManager) RecordRange(routineIndex int) (int, int) {
	calcLen := m.recordLen / m.Number

	var start = routineIndex * calcLen

	if m.recordLen <= start {
		return 0, 0
	}

	var end int
	if routineIndex == m.Number-1 {
		end = m.recordLen
	} else {
		end = (routineIndex + 1) * calcLen
	}
	return start, end
}

func (m *GoroutineTaskManager) Add() {
	m.waitGroup.Add(1)
}

func (m *GoroutineTaskManager) Done() {
	m.grCountMutex.Lock()
	if 0 < m.grCount {
		m.grCount--
		GetGoroutineManager().Release()
	}
	m.grCountMutex.Unlock()

	m.waitGroup.Done()
}

func (m *GoroutineTaskManager) Wait() {
	m.waitGroup.Wait()
}

func (m *GoroutineTaskManager) Run(ctx context.Context, fn func(int) error) error {
	for i := 0; i < m.Number; i++ {
		m.Add()
		go func(thIdx int) {
			start, end := m.RecordRange(thIdx)

			for j := start; j < end; j++ {
				if m.HasError() || ctx.Err() != nil {
					break
				}

				if err := fn(j); err != nil {
					m.SetError(err)
					break
				}
			}

			m.Done()
		}(i)
	}
	m.Wait()

	if ctx.Err() != nil {
		return NewContextIsDone(ctx.Err().Error())
	}
	return nil
}
