package remotehost_test

import (
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

func init() {
	var service *app.LaunchService
	remotehost.ServeLauncher = func(m *herdr.Manager) remotehost.Launcher {
		service = app.NewLaunchService(m)
		service.StartFollowUpWorker() // the queue's delivery worker, as the window starts it
		return app.NewRemoteLauncher(service)
	}
	remotehost.ServeGit = func() remotehost.GitReader { return app.NewRemoteGit() }
	remotehost.ServeQueue = func() remotehost.FollowUps { return app.NewRemoteFollowUps(service) }
}
