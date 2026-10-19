package uiapp

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/schedule"
)

// jobsModal manages the automation jobs（specs/automation.md 的管理面）：
// 列表（下次到期、上次状态、立即运行/启停/删除）+ 新建表单。数据在打开
// 与操作后刷新；运行是异步的，回来时更新状态。
func (a *App) jobsModal(c *ui.Context, t *ui.Theme) {
	if !a.jobsOpen {
		return
	}
	ui.Modal(c, &a.jobsOpen, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconClock).FontSize(15).TextColor(t.TextMuted)
					ui.Text(c, "定时任务").Bold().FontSize(15)
				})
				if iconButton(c, iconRefresh, "刷新", "重新加载任务").Clicked() {
					a.loadJobs()
				}
			})
			if a.jobsErr != "" {
				ui.Text(c, a.jobsErr).TextColor(t.Danger).FontSize(12)
			}
			if len(a.jobs) == 0 {
				ui.Text(c, "（无定时任务）").TextColor(t.TextMuted)
			}
			for i := range a.jobs {
				a.jobRow(c, t, &a.jobs[i])
			}
			a.jobAddForm(c, t)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "关闭").Clicked() {
					a.jobsOpen = false
				}
			})
		})
	})
}

// jobRow renders one job with its actions.
func (a *App) jobRow(c *ui.Context, t *ui.Theme, j *schedule.Job) {
	ui.Column(c).Gap(4).Padding(8).Radius(8).
		Background(t.Surface).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			// 状态图标：正常 ✓（绿）、停用 ■（灰）、上次失败 ✕（红）。
			switch {
			case !j.Enabled:
				ui.Icon(c, iconSquare).FontSize(13).TextColor(t.TextMuted)
			case j.LastStatus != "" && j.LastStatus != "ok":
				ui.Icon(c, iconCircleX).FontSize(13).TextColor(t.Danger)
			default:
				ui.Icon(c, iconCheck).FontSize(13).TextColor(t.Success)
			}
			ui.Text(c, j.ID).Bold()
			ui.Text(c, j.Schedule).TextColor(t.TextMuted)
			if next := schedule.NextDue(*j, time.Now()); !next.IsZero() {
				ui.Text(c, "下次 "+next.Format("01-02 15:04")).TextColor(t.TextMuted).FontSize(12)
			}
			ui.Spacer(c)
			if a.jobRunning == j.ID {
				ui.Spinner(c).FontSize(12)
				ui.Text(c, "运行中…").TextColor(t.TextMuted).FontSize(12)
			} else if textIconButton(c, iconPlay, "运行").Clicked() {
				a.runJob(j.ID)
			}
			label := "停用"
			if !j.Enabled {
				label = "启用"
			}
			if ui.Button(c, label).Clicked() {
				a.toggleJob(j.ID, !j.Enabled)
			}
			if iconButton(c, iconX, "删除 "+j.ID, "删除此任务").Clicked() {
				a.removeJob(j.ID)
			}
		})
		if j.LastRun != (time.Time{}) {
			status := j.LastStatus
			if status == "" {
				status = "—"
			}
			ui.Text(c, "上次 "+j.LastRun.Local().Format("01-02 15:04")+" · "+status).
				TextColor(t.TextMuted).FontSize(12)
		}
	})
}

// jobAddForm is the new-job form：计划/提示/工作区/模型。
func (a *App) jobAddForm(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(8).Padding(8).Radius(8).BorderWidth(1, 1, 1, 1).
		BorderColor(t.Border).Children(func() {
		ui.Text(c, "新建任务").Bold().FontSize(13)
		ui.TextInput(c, &a.jobSchedule).Label("计划（分 时 日 月 周）").Placeholder("*/10 * * * *")
		ui.TextInput(c, &a.jobPrompt).Label("提示").Placeholder("要定期运行的任务描述")
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			ui.TextInput(c, &a.jobWorkspace).Label("工作区").Grow(1)
			if ui.Button(c, "浏览…").Clicked() {
				a.pickJobWorkspace()
			}
		})
		ui.TextInput(c, &a.jobModel).Label("模型（可选，留空用当前设置）")
		if a.jobFormErr != "" {
			ui.Text(c, a.jobFormErr).TextColor(t.Danger).FontSize(12)
		}
		if ui.PrimaryButton(c, "添加").Clicked() {
			a.addJobFromForm()
		}
	})
}

// pickJobWorkspace fills the form's workspace via the directory dialog.
func (a *App) pickJobWorkspace() {
	if ws, err := a.svc.PickWorkspace(a.win); err == nil && ws != "" {
		a.jobWorkspace = ws
	}
}

// loadJobs refreshes the panel's data on the view goroutine.
func (a *App) loadJobs() {
	jobs, err := a.svc.Jobs()
	if err != nil {
		a.jobsErr = err.Error()
		return
	}
	a.jobs = schedule.SortedJobs(jobs)
	a.jobsErr = ""
}

// OpenJobs opens the automation panel, seeded with the current jobs.
func (a *App) OpenJobs() {
	if a.chat.running {
		return
	}
	a.loadJobs()
	a.jobsOpen = true
}

// addJobFromForm validates and stores the form's job.
func (a *App) addJobFromForm() {
	id := "job-" + time.Now().Format("0102-1504")
	job := schedule.Job{ID: id, Schedule: a.jobSchedule, Prompt: a.jobPrompt,
		Workspace: a.jobWorkspace, Model: a.jobModel, Enabled: true}
	if err := a.svc.UpsertJob(job); err != nil {
		a.jobFormErr = err.Error()
		return
	}
	a.jobFormErr = ""
	a.jobSchedule, a.jobPrompt, a.jobWorkspace, a.jobModel = "", "", "", ""
	a.loadJobs()
}

// runJob fires one job now (async; the panel shows 运行中… until refresh).
func (a *App) runJob(id string) {
	a.jobRunning = id
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, _ = a.svc.RunJobNow(ctx, id)
		a.update(func() {
			a.jobRunning = ""
			a.loadJobs()
		})
	}()
}

func (a *App) toggleJob(id string, on bool) {
	if _, err := a.svc.ToggleJob(id, on); err != nil {
		a.jobsErr = err.Error()
	}
	a.loadJobs()
}

func (a *App) removeJob(id string) {
	if _, err := a.svc.RemoveJob(id); err != nil {
		a.jobsErr = err.Error()
	}
	a.loadJobs()
}
