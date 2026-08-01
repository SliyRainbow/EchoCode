package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/applicense"
	"github.com/SliyRainbow/EchoCode/internal/appupdate"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetAppInfo() appupdate.AppInfo {
	if a.updater == nil {
		return appupdate.AppInfo{Name: "EchoCode", Version: appVersion, ConfigPath: runtimeSettingsPath()}
	}
	return a.updater.Info(runtimeSettingsPath())
}

func (a *App) GetOpenSourceLicenses() []applicense.Notice {
	return applicense.Catalog()
}

func (a *App) GetUpdateState() appupdate.State {
	if a.updater == nil {
		return appupdate.State{CurrentVersion: appVersion, Status: "error", Message: "更新服务未初始化"}
	}
	return a.updater.State()
}

func (a *App) CheckForUpdates() (appupdate.State, error) {
	if a.updater == nil {
		return appupdate.State{}, errors.New("更新服务未初始化")
	}
	ctx, cancel := context.WithTimeout(a.appContext(), 45*time.Second)
	defer cancel()
	return a.updater.Check(ctx, true)
}

func (a *App) DownloadUpdate() (appupdate.State, error) {
	if a.updater == nil {
		return appupdate.State{}, errors.New("更新服务未初始化")
	}
	ctx, cancel := context.WithTimeout(a.appContext(), 30*time.Minute)
	defer cancel()
	return a.updater.Download(ctx)
}

func (a *App) InstallUpdate() (appupdate.State, error) {
	if a.updater == nil {
		return appupdate.State{}, errors.New("更新服务未初始化")
	}
	state, err := a.updater.LaunchInstaller()
	if err != nil {
		return state, err
	}
	ctx := a.ctx
	if ctx != nil {
		go func() {
			time.Sleep(180 * time.Millisecond)
			wruntime.Quit(ctx)
		}()
	}
	return state, nil
}

func (a *App) OpenUpdateReleasePage() error {
	if a.updater == nil {
		return errors.New("更新服务未初始化")
	}
	target := a.updater.State().ReleaseURL
	if target == "" {
		target = a.updater.Info(runtimeSettingsPath()).RepositoryURL + "/releases"
	}
	return a.OpenURLInSystemBrowser(target)
}

func (a *App) OpenAppRepositoryPage() error {
	if a.updater == nil {
		return a.OpenURLInSystemBrowser("https://github.com/SliyRainbow/EchoCode")
	}
	return a.OpenURLInSystemBrowser(a.updater.Info(runtimeSettingsPath()).RepositoryURL)
}

func (a *App) RevealAppExecutable() error {
	info := a.GetAppInfo()
	if info.ExecutablePath == "" {
		return errors.New("无法确定程序位置")
	}
	return revealDesktopFile(info.ExecutablePath)
}

func (a *App) RevealAppConfigFile() error {
	info := a.GetAppInfo()
	if info.ConfigPath == "" {
		return errors.New("无法确定配置文件位置")
	}
	if _, err := os.Stat(info.ConfigPath); err == nil {
		return revealDesktopFile(info.ConfigPath)
	}
	return openDesktopFile(filepath.Dir(info.ConfigPath))
}

func (a *App) checkForUpdatesOnStartup(parent context.Context, autoDownload bool) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	state, err := a.updater.Check(ctx, false)
	cancel()
	if err != nil || !autoDownload || !state.UpdateAvailable || state.DownloadURL == "" || state.Status == "downloaded" {
		return
	}
	downloadCtx, downloadCancel := context.WithTimeout(parent, 30*time.Minute)
	defer downloadCancel()
	_, _ = a.updater.Download(downloadCtx)
}

func (a *App) appContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
