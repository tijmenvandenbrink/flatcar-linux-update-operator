package agent_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:funlen,cyclop // Just many subtests.
func Test_Creating_new_agent(t *testing.T) {
	t.Parallel()

	t.Run("returns_agent_when_all_dependencies_are_satisfied", func(t *testing.T) {
		t.Parallel()

		client, err := agent.New(testConfig(t))
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		if client == nil {
			t.Fatalf("Client should be returned when creating agent succeeds")
		}
	})

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("no_clientset_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutClientset := testConfig(t)
			configWithoutClientset.Clientset = nil

			client, err := agent.New(configWithoutClientset)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("no_status_receiver_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig(t)
			configWithoutStatusReceiver.StatusReceiver = nil

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("no_rebooter_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig(t)
			configWithoutStatusReceiver.Rebooter = nil

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("empty_node_name_is_given", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig(t)
			configWithoutStatusReceiver.NodeName = ""

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})
	})
}

// Expose klog flags to be able to increase verbosity for agent logs.
func TestMain(m *testing.M) {
	testFlags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	klog.InitFlags(testFlags)

	if err := testFlags.Parse([]string{"-v=10"}); err != nil {
		fmt.Printf("Failed parsing flags: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

//nolint:funlen,cyclop // Just many test cases.
func Test_Running_agent(t *testing.T) {
	t.Parallel()

	t.Run("reads_host_configuration_by", func(t *testing.T) {
		t.Parallel()

		configWithTestFilesPrefix := testConfig(t)

		expectedGroup := "configuredGroup"
		expectedOSID := "testID"
		expectedVersion := "testVersion"

		files := map[string]string{
			"/usr/share/flatcar/update.conf": "GROUP=" + expectedGroup,
			"/etc/os-release":                fmt.Sprintf("ID=%s\nVERSION=%s", expectedOSID, expectedVersion),
		}

		createTestFiles(t, files, configWithTestFilesPrefix.HostFilesPrefix)

		client, err := agent.New(configWithTestFilesPrefix)
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		ctx, cancel := context.WithTimeout(contextWithDeadline(t), 1*time.Second)

		done := make(chan error)

		t.Cleanup(cancel)

		go func() {
			done <- client.Run(ctx.Done())
		}()

		t.Run("reading_OS_ID_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertLabelValue(ctx, t, done, configWithTestFilesPrefix, constants.LabelID, expectedOSID)
		})

		t.Run("reading_Flatcar_version_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertLabelValue(ctx, t, done, configWithTestFilesPrefix, constants.LabelVersion, expectedVersion)
		})

		t.Run("reading_Flatcar_group_from_update_configuration_file_in_usr_directory", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent
			// read /etc/flatcar/update.conf or /usr/share/flatcar/update.conf.
			assertLabelValue(ctx, t, done, configWithTestFilesPrefix, constants.LabelGroup, expectedGroup)
		})
	})

	t.Run("prefers_Flatcar_group_from_etc_over_usr", func(t *testing.T) {
		t.Parallel()

		configWithTestFilesPrefix := testConfig(t)

		expectedGroup := "configuredGroup"

		files := map[string]string{
			"/usr/share/flatcar/update.conf": "GROUP=testGroup",
			"/etc/flatcar/update.conf":       "GROUP=" + expectedGroup,
			"/etc/os-release":                "",
		}

		createTestFiles(t, files, configWithTestFilesPrefix.HostFilesPrefix)

		client, err := agent.New(configWithTestFilesPrefix)
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		ctx, cancel := context.WithTimeout(contextWithDeadline(t), 1*time.Second)

		done := make(chan error)

		t.Cleanup(cancel)

		go func() {
			done <- client.Run(ctx.Done())
		}()

		assertLabelValue(ctx, t, done, configWithTestFilesPrefix, constants.LabelGroup, expectedGroup)
	})

	t.Run("updates_associated_Node_object_with_host_information_by", func(t *testing.T) {
		t.Parallel()

		// t.Log(updatedNode.Annotations[constants.AnnotationRebootNeeded])
		// t.Log(updatedNode.Annotations[constants.AnnotationRebootInProgress])
		// t.Log(updatedNode.Labels[constants.LabelRebootNeeded])
		t.Run("setting_OS_ID_label", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("setting_Flatcar_group_label", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("setting_Flatcar_version_label", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("Flatcar_update_configuration_file_does_not_exist", func(t *testing.T) {
			t.Parallel()

			configWithNoHostFiles := testConfig(t)

			client, err := agent.New(configWithNoHostFiles)
			if err != nil {
				t.Fatalf("Unexpected error creating new agent: %v", err)
			}

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			done := make(chan error)
			go func() {
				done <- client.Run(ctx.Done())
			}()

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-done:
				if err == nil {
					t.Fatalf("Expected agent to return an error")
				}
			}
		})

		t.Run("configured_Node_does_not_exist", func(t *testing.T) {
			configWithTestFilesPrefix := testConfig(t)
			configWithTestFilesPrefix.Clientset = fake.NewSimpleClientset()

			files := map[string]string{
				"/usr/share/flatcar/update.conf": "GROUP=imageGroup",
				"/etc/os-release":                "ID=testID\nVERSION=testVersion",
			}

			createTestFiles(t, files, configWithTestFilesPrefix.HostFilesPrefix)

			client, err := agent.New(configWithTestFilesPrefix)
			if err != nil {
				t.Fatalf("Unexpected error creating new agent: %v", err)
			}

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			done := make(chan error)
			go func() {
				done <- client.Run(ctx.Done())
			}()

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-done:
				if err == nil {
					t.Fatalf("Expected agent to return an error")
				}

				if !apierrors.IsNotFound(err) {
					t.Fatalf("Expected Node not found error when running agent, got: %v", err)
				}
			}
		})

		t.Run("reading_OS_information_fails_by", func(t *testing.T) {
			t.Run("", func(t *testing.T) {})
			t.Run("", func(t *testing.T) {})
			t.Run("", func(t *testing.T) {})
			t.Run("", func(t *testing.T) {})
		})
	})
}

func testConfig(t *testing.T) *agent.Config {
	t.Helper()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testNodeName",
			// TODO: Fix code to handle Node objects with no labels and annotations?
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}

	return &agent.Config{
		Clientset:       fake.NewSimpleClientset(node),
		StatusReceiver:  &mockStatusReceiver{},
		Rebooter:        &mockRebooter{},
		NodeName:        node.Name,
		HostFilesPrefix: t.TempDir(),
	}
}

type mockStatusReceiver struct{}

func (m *mockStatusReceiver) ReceiveStatuses(rcvr chan<- updateengine.Status, stop <-chan struct{}) {}

type mockRebooter struct{}

func (m *mockRebooter) Reboot(bool) {}

func contextWithDeadline(t *testing.T) context.Context {
	t.Helper()

	deadline, ok := t.Deadline()
	if !ok {
		return context.Background()
	}

	// Arbitrary amount of time to let tests exit cleanly before main process terminates.
	timeoutGracePeriod := 10 * time.Second

	ctx, cancel := context.WithDeadline(context.Background(), deadline.Truncate(timeoutGracePeriod))
	t.Cleanup(cancel)

	return ctx
}

func createTestFiles(t *testing.T, filesContentByPath map[string]string, prefix string) {
	t.Helper()

	for path, content := range filesContentByPath {
		pathWithPrefix := filepath.Join(prefix, path)

		dir := filepath.Dir(pathWithPrefix)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("Failed creating directory %q: %v", dir, err)
		}

		if err := os.WriteFile(pathWithPrefix, []byte(content), 0o600); err != nil {
			t.Fatalf("Failed creating file %q: %v", pathWithPrefix, err)
		}
	}
}

//nolint:lll // Not worth refactoring yet.
func assertLabelValue(ctx context.Context, t *testing.T, done <-chan error, config *agent.Config, key, expectedValue string) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)

	for {
		select {
		case err := <-done:
			t.Fatalf("Agent stopped prematurely: %v", err)
		case <-ctx.Done():
			t.Fatalf("Timed out waiting for label %q", key)
		case <-ticker.C:
			nodeClient := config.Clientset.CoreV1().Nodes()

			updatedNode, err := nodeClient.Get(ctx, config.NodeName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed getting Node object %q: %v", config.NodeName, err)
			}

			value, ok := updatedNode.Labels[key]
			if !ok {
				continue
			}

			if value != expectedValue {
				t.Fatalf("Expected value %q for key %q, got %q", expectedValue, key, value)
			}

			return
		}
	}
}
