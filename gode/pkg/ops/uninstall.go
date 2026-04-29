package ops

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
)

type Message struct {
	r release.Release
	e error
}

func UninstallContext(ctx context.Context, actionConfig *action.Configuration, target string) error {
	responseChan := doUninstall(actionConfig, target)
	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled")
	case msg := <-responseChan:
		log.Printf("Uninsall respose: %+v, %+v", msg.r, msg.e)
		if msg.e != nil {
			return msg.e
		}
		return nil
	}
}

func Uninstall(actionConfig *action.Configuration, target string) error {
	responseChan := doUninstall(actionConfig, target)
	msg := <-responseChan
	slog.Info("Ran uninstall", "release", target)
	if msg.e != nil {
		return msg.e
	}
	return nil

}

func doUninstall(actionConfig *action.Configuration, target string) chan Message {
	uninstallClient := action.NewUninstall(actionConfig)
	uninstallClient.IgnoreNotFound = true
	uninstallClient.Timeout = time.Second * 60
	uninstallClient.WaitStrategy = kube.StatusWatcherStrategy

	responseChan := make(chan Message, 1)
	go func() {
		defer close(responseChan)
		slog.Info("Uninstalling", "release", target)
		resp, err := uninstallClient.Run(target)
		var msg Message = Message{}
		if err != nil {
			msg.e = fmt.Errorf("failed to uninstall release %s: %w", target, err)
		}
		if releasev1 := ToReleaseV1(resp.Release); releasev1 != nil {
			if releasev1.Info.Status == common.StatusFailed {
				msg.e = fmt.Errorf("uninstallation processes resulted in failure: %s", releasev1.Info.Description)
			}
		}
		responseChan <- msg
	}()
	return responseChan
}
