package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"helm.sh/helm/v4/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func StreamPodLogs(ctx context.Context, settings *cli.EnvSettings, cs *kubernetes.Clientset, releaseName string) error {
	podList, err := listLoop(ctx, releaseName, cs, settings)
	if err != nil {
		return err
	}
	errChan := make(chan error, 1)
	for _, pod := range podList.Items {
		go func() {
			var err error
			req := cs.CoreV1().Pods(settings.Namespace()).GetLogs(pod.Name, &corev1.PodLogOptions{Follow: true})
			if req.Error() != nil {
				err = req.Error()
			}
			reader, err := req.Stream(ctx)
			if err != nil {
				slog.Error("failed to stream logs", "pod", pod.Name, "error", err)
				errChan <- err
			}
			_, err = os.Stderr.ReadFrom(reader)

			if err != nil {
				slog.Error("failed to listen to logs", "pod", pod.Name, "error", err)
				errChan <- err
			}

		}()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errChan:
		return err
	}
}

func ForwardPort(ctx context.Context, settings *cli.EnvSettings, cs *kubernetes.Clientset, releaseName string, port int) error {
	podList, err := listLoop(ctx, releaseName, cs, settings)
	if err != nil {
		return err
	}
	errChan := make(chan error, 1)
	for _, pod := range podList.Items {
		go func() {
			slog.Info("Trying to forward port", "pod", pod.Name, "port", port)
			cmd := exec.CommandContext(ctx, "kubectl", "port-forward", fmt.Sprintf("pods/%s", pod.Name), fmt.Sprintf("%d:%d", port, port))
			if err := cmd.Run(); err != nil {
				errChan <- fmt.Errorf("failed to forward port for pod %s to %d: %w", pod.GetName(), port, err)
			} else {
				slog.Info("Forwarded port", "pod", pod.Name, "port", port)
			}
		}()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errChan:
		return err
	}
}

func listLoop(ctx context.Context, releaseName string, cs *kubernetes.Clientset, settings *cli.EnvSettings) (*corev1.PodList, error) {
	label := fmt.Sprintf("app.kubernetes.io/instance=%s", releaseName)
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context canelled")
	default:
	}
	listOptions := metav1.ListOptions{
		LabelSelector: label,
		FieldSelector: "status.phase=Running",
	}
	for range 10 {
		podList, err := cs.CoreV1().Pods(settings.Namespace()).List(ctx, listOptions)
		if err != nil {
			return nil, err
		}
		if len(podList.Items) != 0 {
			return podList, nil
		}
		slog.Info("No pods found. Retrying", "label", label, "release", releaseName)
		time.Sleep(time.Second * 5)
	}

	return nil, fmt.Errorf("no pod found for release %s", releaseName)
}

func GetClientSet(settings *cli.EnvSettings) (*kubernetes.Clientset, error) {
	kubeConfig, err := clientcmd.BuildConfigFromFlags("", settings.KubeConfig)
	if err != nil {
		return nil, err
	}
	clientSet, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}
