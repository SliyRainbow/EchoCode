package main

import (
	"errors"
	"fmt"

	"github.com/SliyRainbow/EchoCode/internal/automation"
)

func (a *App) GetAutomationState() automation.State {
	if a.automations == nil {
		return automation.State{Tasks: []automation.Task{}, Error: a.automationUnavailable().Error()}
	}
	return a.automations.State()
}

func (a *App) automationUnavailable() error {
	if a.automationLoadError != nil {
		return fmt.Errorf("自动化数据读取失败，原文件已保留：%w", a.automationLoadError)
	}
	return errors.New("自动化服务未初始化")
}

func (a *App) SaveAutomationTask(task automation.Task) (automation.State, error) {
	if a.automations == nil {
		return a.GetAutomationState(), a.automationUnavailable()
	}
	return a.automations.Save(task)
}

func (a *App) DeleteAutomationTask(taskID string) (automation.State, error) {
	if a.automations == nil {
		return a.GetAutomationState(), a.automationUnavailable()
	}
	return a.automations.Delete(taskID)
}

func (a *App) SetAutomationTaskEnabled(taskID string, enabled bool) (automation.State, error) {
	if a.automations == nil {
		return a.GetAutomationState(), a.automationUnavailable()
	}
	return a.automations.SetEnabled(taskID, enabled)
}

func (a *App) RunAutomationTaskNow(taskID string) (automation.State, error) {
	if a.automations == nil {
		return a.GetAutomationState(), a.automationUnavailable()
	}
	return a.automations.RunNow(taskID)
}

func (a *App) StopAutomationTask(taskID string) (automation.State, error) {
	if a.automations == nil {
		return a.GetAutomationState(), a.automationUnavailable()
	}
	state := a.automations.State()
	chatTaskID := ""
	for _, task := range state.Tasks {
		if task.ID == taskID && task.LastRun != nil {
			chatTaskID = task.LastRun.ChatTaskID
			break
		}
	}
	if chatTaskID == "" {
		return state, errors.New("自动化任务当前没有可停止的 Agent")
	}
	state, err := a.automations.MarkStopping(taskID)
	if err != nil {
		canStillStop := false
		for _, task := range state.Tasks {
			if task.ID == taskID && task.LastRun != nil && task.LastRun.Status == "running" && task.LastRun.ChatTaskID == chatTaskID {
				canStillStop = true
			}
		}
		if !canStillStop {
			return state, err
		}
	}
	if !a.StopChatMessage(chatTaskID) {
		a.automations.CompleteChatTask(chatTaskID, "failed", "Agent 任务已结束或不存在")
		return a.automations.State(), errors.New("Agent 任务已结束或不存在")
	}
	return state, err
}
