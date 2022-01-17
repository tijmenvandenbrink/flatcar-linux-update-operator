package agent_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	fakecorev1 "k8s.io/client-go/kubernetes/typed/core/v1/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

func Test_Creating_new_agent(t *testing.T) {
	t.Parallel()

	t.Run("returns_agent_when_all_dependencies_are_satisfied", func(t *testing.T) {
		t.Parallel()

		client, err := agent.New(validTestConfig(t))
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		if client == nil {
			t.Fatalf("Client should be returned when creating agent succeeds")
		}
	})

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		cases := map[string]func(*agent.Config){
			"no_clientset_is_configured":       func(c *agent.Config) { c.Clientset = nil },
			"no_status_receiver_is_configured": func(c *agent.Config) { c.StatusReceiver = nil },
			"no_rebooter_is_configured":        func(c *agent.Config) { c.Rebooter = nil },
			"empty_node_name_is_given":         func(c *agent.Config) { c.NodeName = "" },
		}

		for n, mutateConfigF := range cases {
			mutateConfigF := mutateConfigF

			t.Run(n, func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)
				mutateConfigF(testConfig)

				client, err := agent.New(testConfig)
				if err == nil {
					t.Fatalf("Expected error creating new agent")
				}

				if client != nil {
					t.Fatalf("No client should be returned when New failed")
				}
			})
		}
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

//nolint:funlen,cyclop,gocognit,gocyclo // Just many test cases.
func Test_Running_agent(t *testing.T) {
	t.Parallel()

	t.Run("reads_host_configuration_by", func(t *testing.T) {
		t.Parallel()

		testConfig := validTestConfig(t)

		expectedGroup := "configuredGroup"
		expectedOSID := "testID"
		expectedVersion := "testVersion"

		files := map[string]string{
			"/usr/share/flatcar/update.conf": "GROUP=" + expectedGroup,
			"/etc/os-release":                fmt.Sprintf("ID=%s\nVERSION=%s", expectedOSID, expectedVersion),
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
		t.Cleanup(cancel)

		done := runAgent(ctx, t, testConfig)

		t.Run("reading_OS_ID_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertLabelValue(ctx, t, done, testConfig, constants.LabelID, expectedOSID)
		})

		t.Run("reading_Flatcar_version_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertLabelValue(ctx, t, done, testConfig, constants.LabelVersion, expectedVersion)
		})

		t.Run("reading_Flatcar_group_from_update_configuration_file_in_usr_directory", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent
			// read /etc/flatcar/update.conf or /usr/share/flatcar/update.conf.
			assertLabelValue(ctx, t, done, testConfig, constants.LabelGroup, expectedGroup)
		})
	})

	t.Run("prefers_Flatcar_group_from_etc_over_usr", func(t *testing.T) {
		t.Parallel()

		testConfig := validTestConfig(t)

		expectedGroup := "configuredGroup"

		files := map[string]string{
			"/etc/flatcar/update.conf": "GROUP=" + expectedGroup,
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		ctx, cancel := context.WithTimeout(contextWithDeadline(t), 1*time.Second)
		t.Cleanup(cancel)

		done := runAgent(ctx, t, testConfig)

		assertLabelValue(ctx, t, done, testConfig, constants.LabelGroup, expectedGroup)
	})

	t.Run("ignores_when_etcd_update_configuration_file_does_not_exist", func(t *testing.T) {
		t.Parallel()

		testConfig := validTestConfig(t)

		expectedGroup := "imageGroup"

		files := map[string]string{
			"/usr/lib/flatcar/update.conf": "GROUP=" + expectedGroup,
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		updateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/etc/flatcar/update.conf")

		if err := os.Remove(updateConfigFile); err != nil {
			t.Fatalf("Failed removing test file: %v", err)
		}

		ctx, cancel := context.WithTimeout(contextWithDeadline(t), 1*time.Second)
		t.Cleanup(cancel)

		done := runAgent(ctx, t, testConfig)

		assertLabelValue(ctx, t, done, testConfig, constants.LabelGroup, expectedGroup)
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

		t.Run("configured_Node_does_not_exist", func(t *testing.T) {
			configWithNoNodeObject := validTestConfig(t)
			configWithNoNodeObject.Clientset = fake.NewSimpleClientset()

			err := getAgentRunningError(t, configWithNoNodeObject)
			if !apierrors.IsNotFound(err) {
				t.Fatalf("Expected Node not found error when running agent, got: %v", err)
			}
		})

		t.Run("reading_OS_information_fails_because", func(t *testing.T) {
			t.Run("usr_update_config_file_is_not_available", func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)

				usrUpdateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/usr/share/flatcar/update.conf")

				if err := os.Remove(usrUpdateConfigFile); err != nil {
					t.Fatalf("Failed removing test config file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})

			t.Run("etc_update_config_file_is_not_readable", func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)

				updateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/etc/flatcar/update.conf")

				if err := os.Chmod(updateConfigFile, 0o000); err != nil {
					t.Fatalf("Failed changing mode for test file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})

			t.Run("os_release_file_is_not_readable", func(t *testing.T) {
				testConfig := validTestConfig(t)

				osReleasePath := filepath.Join(testConfig.HostFilesPrefix, "/etc/os-release")

				if err := os.Chmod(osReleasePath, 0o000); err != nil {
					t.Fatalf("Failed changing mode for test file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})
		})

		for name, method := range map[string]string{
			"getting_existing_Node_annotations_fails":                 "get",
			"setting_initial_set_of_Node_annotation_and_labels_fails": "update",
		} {
			method := method

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)

				node, err := testConfig.Clientset.CoreV1().Nodes().Get(context.TODO(), testConfig.NodeName, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("Failed getting test Node object: %v", err)
				}

				firstCall := true

				expectedError := errors.New("Error node operation " + method)

				returnErrOnSecondCallF := func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					if firstCall {
						firstCall = false

						return true, node, nil
					}

					return true, nil, expectedError
				}

				testConfig.Clientset.CoreV1().(*fakecorev1.FakeCoreV1).PrependReactor(method, "nodes", returnErrOnSecondCallF)

				err = getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})
		}

		t.Run("waiting_for_not_ok_to_reboot_annotation_fails_because", func(t *testing.T) {
			t.Parallel()

			t.Run("getting_Node_object_fails", func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)

				node, err := testConfig.Clientset.CoreV1().Nodes().Get(context.TODO(), testConfig.NodeName, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("Failed getting test Node object: %v", err)
				}

				callCounter := 0
				// 1. Updating info labels. TODO: Could be done with patch instead.
				// 2. Checking made unschedulable.
				// 3. Updating annotations and labels.
				failingGetCall := 3

				expectedError := errors.New("Error getting node")

				returnErrOnFailingCallF := func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					// TODO: Make implementation smarter.
					if callCounter != failingGetCall {
						callCounter++

						return true, node, nil
					}

					return true, nil, expectedError
				}

				testConfig.Clientset.CoreV1().(*fakecorev1.FakeCoreV1).PrependReactor("get", "*", returnErrOnFailingCallF)

				err = getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})

			t.Run("creating_Node_watcher_fails", func(t *testing.T) {
				t.Parallel()

				testConfig := validTestConfig(t)

				node := okToRebootNode()

				testConfig.Clientset = fake.NewSimpleClientset(node)

				expectedError := errors.New("creating watcher")
				f := func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
					return true, nil, expectedError
				}

				testConfig.Clientset.CoreV1().(*fakecorev1.FakeCoreV1).PrependWatchReactor("*", f)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q running agent, got %q", expectedError, err)
				}
			})

			t.Run("watching_Node", func(t *testing.T) {
				t.Parallel()

				cases := map[string]struct {
					watchEvent    func(*watch.FakeWatcher)
					expectedError string
				}{
					"returns_watch_error": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Error(nil) },
						expectedError: "watching node",
					},
					"returns_object_deleted_error": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Delete(nil) },
						expectedError: "node was deleted",
					},
					"returns_unknown_event_type": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Action(watch.Bookmark, nil) },
						expectedError: "unknown event type",
					},
				}

				for n, testCase := range cases {
					testCase := testCase

					t.Run(n, func(t *testing.T) {
						t.Parallel()

						testConfig := validTestConfig(t)

						node := okToRebootNode()

						testClient := fake.NewSimpleClientset(node)
						testConfig.Clientset = testClient
						watcher := watch.NewFake()
						testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

						go testCase.watchEvent(watcher)

						err := getAgentRunningError(t, testConfig)
						if err == nil {
							t.Fatalf("Expected error running agent")
						}

						if !strings.Contains(err.Error(), testCase.expectedError) {
							t.Fatalf("Expected error %q, got %q", testCase.expectedError, err)
						}
					})
				}
			})
		})

		t.Run("marking_Node_schedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig := validTestConfig(t)

			node := nodeMadeUnschedulable()

			testClient := fake.NewSimpleClientset(node)
			testConfig.Clientset = testClient
			watcher := watch.NewFake()
			testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as schedulable, make error occur.
				return true, nil, expectedError
			}

			testClient.Fake.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			// Establish watch for node updates.
			errCh := runAgent(ctx, t, testConfig)

			// Mock operator action.
			node.Annotations[constants.AnnotationOkToReboot] = constants.False

			watcher.Modify(node)

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-errCh:
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q, got %q", expectedError, err)
				}
			}
		})

		t.Run("updating_Node_annotations_after_marking_Node_schedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig := validTestConfig(t)

			node := nodeMadeUnschedulable()

			testClient := fake.NewSimpleClientset(node)
			testConfig.Clientset = testClient
			watcher := watch.NewFake()
			testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if node.Annotations[constants.AnnotationAgentMadeUnschedulable] != constants.False {
					return true, node, nil
				}

				// If node is about to be annotated as no longer made unschedulable by agent, make error occur.
				return true, nil, expectedError
			}

			testClient.Fake.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			// Establish watch for node updates.
			errCh := runAgent(ctx, t, testConfig)

			// Mock operator action.
			node.Annotations[constants.AnnotationOkToReboot] = constants.False

			watcher.Modify(node)

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-errCh:
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q, got %q", expectedError, err)
				}
			}
		})

		t.Run("getting_Node_object_after_ok_to_reboot_is_given", func(t *testing.T) {
			t.Parallel()

			testConfig := validTestConfig(t)

			node := testNode()

			testClient := fake.NewSimpleClientset(node)
			testConfig.Clientset = testClient
			watcher := watch.NewFakeWithChanSize(1, true)

			// Mock operator action.
			updatedNode := node.DeepCopy()
			updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.True
			updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
			watcher.Modify(updatedNode)

			testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

			callCounter := 0
			// 1. Updating info labels. TODO: Could be done with patch instead.
			// 2. Checking made unschedulable.
			// 3. Updating annotations and labels.
			// 4. Getting initial state while waiting for ok-to-reboot.
			// 5. Getting node object to mark it unscheduable etc.
			failingGetCall := 5

			expectedError := errors.New("Error getting node")

			returnErrOnFailingCallF := func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
				// TODO: Make implementation smarter.
				if callCounter != failingGetCall {
					callCounter++

					return true, node, nil
				}

				return true, nil, expectedError
			}

			testClient.Fake.PrependReactor("get", "*", returnErrOnFailingCallF)

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			// Establish watch for node updates.
			errCh := runAgent(ctx, t, testConfig)

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-errCh:
				if err == nil {
					t.Fatalf("Expected error %q, got %q", expectedError, err)
				}
			}
		})

		t.Run("setting_reboot_in_progress_annotation_fails", func(t *testing.T) {
			t.Parallel()
			testConfig := validTestConfig(t)

			node := testNode()

			testClient := fake.NewSimpleClientset(node)
			testConfig.Clientset = testClient
			watcher := watch.NewFakeWithChanSize(1, true)

			// Mock operator action.
			updatedNode := node.DeepCopy()
			updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.True
			updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
			watcher.Modify(updatedNode)

			testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

			expectedError := errors.New("Error marking node as unschedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if v, ok := node.Annotations[constants.AnnotationRebootInProgress]; !ok || v != constants.True {
					return true, node, nil
				}

				// If node is about to be marked as reboot is in progress, make error occur.
				return true, nil, expectedError
			}

			testClient.Fake.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			// Establish watch for node updates.
			errCh := runAgent(ctx, t, testConfig)

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-errCh:
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q, got %q", expectedError, err)
				}
			}
		})

		t.Run("marking_Node_unschedulable_fails", func(t *testing.T) {
			t.Parallel()
			testConfig := validTestConfig(t)

			node := testNode()

			testClient := fake.NewSimpleClientset(node)
			testConfig.Clientset = testClient
			watcher := watch.NewFakeWithChanSize(1, true)

			// Mock operator action.
			updatedNode := node.DeepCopy()
			updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.True
			updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
			watcher.Modify(updatedNode)

			testClient.Fake.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

			expectedError := errors.New("Error marking node as unschedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if !node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as unschedulable, make error occur.
				return true, nil, expectedError
			}

			testClient.Fake.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			// Establish watch for node updates.
			errCh := runAgent(ctx, t, testConfig)

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-errCh:
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q, got %q", expectedError, err)
				}
			}
		})
	})
}

func validTestConfig(t *testing.T) *agent.Config {
	t.Helper()

	node := testNode()

	files := map[string]string{
		"/usr/share/flatcar/update.conf": "GROUP=imageGroup",
		"/etc/flatcar/update.conf":       "GROUP=configuredGroup",
		"/etc/os-release":                "ID=testID\nVERSION=testVersion",
	}

	hostFilesPrefix := t.TempDir()

	createTestFiles(t, files, hostFilesPrefix)

	return &agent.Config{
		Clientset:       fake.NewSimpleClientset(node),
		StatusReceiver:  &mockStatusReceiver{},
		Rebooter:        &mockRebooter{},
		NodeName:        node.Name,
		HostFilesPrefix: hostFilesPrefix,
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

func runAgent(ctx context.Context, t *testing.T, config *agent.Config) <-chan error {
	t.Helper()

	client, err := agent.New(config)
	if err != nil {
		t.Fatalf("Unexpected error creating new agent: %v", err)
	}

	done := make(chan error)

	go func() {
		done <- client.Run(ctx.Done())
	}()

	return done
}

func getAgentRunningError(t *testing.T, config *agent.Config) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("Expected agent to exit before deadline")
	case err := <-runAgent(ctx, t, config):
		return err
	}

	return nil
}

func okToRebootNode() *corev1.Node {
	node := testNode()

	node.Annotations[constants.AnnotationOkToReboot] = constants.True

	return node
}

func nodeMadeUnschedulable() *corev1.Node {
	node := testNode()

	node.Annotations[constants.AnnotationOkToReboot] = constants.True
	node.Annotations[constants.AnnotationAgentMadeUnschedulable] = constants.True
	node.Spec.Unschedulable = true

	return node
}

func testNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testNodeName",
			// TODO: Fix code to handle Node objects with no labels and annotations?
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}
}
