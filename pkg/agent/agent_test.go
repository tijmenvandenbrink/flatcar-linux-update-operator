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
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

func Test_Creating_new_agent(t *testing.T) {
	t.Parallel()

	t.Run("succeeds_when_all_dependencies_are_satisfied", func(t *testing.T) {
		t.Parallel()

		validConfig, _, _ := validTestConfig(t, testNode())
		client, err := agent.New(validConfig)
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		if client == nil {
			t.Fatalf("Client should be returned when creating agent succeeds")
		}
	})

	t.Run("fails_when", func(t *testing.T) {
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

				testConfig, _, _ := validTestConfig(t, testNode())
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

//nolint:funlen,cyclop,gocognit // Just many test cases.
func Test_Running_agent(t *testing.T) {
	t.Parallel()

	t.Run("reads_host_configuration_by", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, testNode())

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

		testConfig, _, _ := validTestConfig(t, testNode())

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

	t.Run("ignores_when_etc_update_configuration_file_does_not_exist", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, testNode())

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

	t.Run("on_start", func(t *testing.T) {
		t.Run("updates_associated_Node_object_with_host_information_by", func(t *testing.T) {
			t.Parallel()

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

		t.Run("sets_reboot_in_progress,_reboot_needed_annotations_and_reboot_needed_label_to_false", func(t *testing.T) {
			t.Parallel()

			// t.Log(updatedNode.Annotations[constants.AnnotationRebootNeeded])
			// t.Log(updatedNode.Annotations[constants.AnnotationRebootInProgress])
			// t.Log(updatedNode.Labels[constants.LabelRebootNeeded])
		})
	})

	t.Run("waits_for_not_ok_to_reboot_annotation_from_operator_after_updating_node_information", func(t *testing.T) {
		t.Parallel()
	})

	t.Run("marks_node_as_schedulable_if_it_has_agent_made_unschedulable_annotation", func(t *testing.T) {
		t.Parallel()
	})

	t.Run("after_getting_not_ok_to_reboot_annotation", func(t *testing.T) {
		t.Parallel()

		t.Run("updates_node_information_when_update_enging_produces_updated_status", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("waits_for_ok_to_reboot_annotation_from_operator", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("after_getting_ok_to_reboot_annotation", func(t *testing.T) {
		t.Parallel()

		t.Run("marks_node_as_unschedulable_by", func(t *testing.T) {
			t.Parallel()

			t.Run("setting_unschedulable_field_on_Node_object", func(t *testing.T) {
				t.Parallel()
			})

			t.Run("setting_reboot_in_progress_and_agent_made_unschedulable_annotations_to_true", func(t *testing.T) {
				t.Parallel()
			})
		})
	})

	t.Run("after_marking_node_as_unschedulable", func(t *testing.T) {
		t.Parallel()

		t.Run("remove_pods_scheduled_on_running_node", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("waits_for_all_pods_scheduled_on_running_node_to_terminate", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("after_draining_node", func(t *testing.T) {
		t.Parallel()

		t.Run("triggers_a_reboot", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("waits_until_termination_signal_comes", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("logs_error_when", func(t *testing.T) {
		// TODO: Those are not hard errors, we should probably test that the tests are logged at least.
		// Alternatively we can test that those errros do not cause agent to exit.
		t.Run("waiting_for_ok_to_reboot_annotation_fails_by", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("removing_pod_on_node_fails", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("stops_with_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("configured_Node_does_not_exist", func(t *testing.T) {
			configWithNoNodeObject, _, _ := validTestConfig(t, testNode())
			configWithNoNodeObject.Clientset = fake.NewSimpleClientset()

			err := getAgentRunningError(t, configWithNoNodeObject)
			if !apierrors.IsNotFound(err) {
				t.Fatalf("Expected Node not found error when running agent, got: %v", err)
			}
		})

		t.Run("reading_OS_information_fails_because", func(t *testing.T) {
			t.Run("usr_update_config_file_is_not_available", func(t *testing.T) {
				t.Parallel()

				testConfig, _, _ := validTestConfig(t, testNode())

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

				testConfig, _, _ := validTestConfig(t, testNode())

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
				testConfig, _, _ := validTestConfig(t, testNode())

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

				testConfig, node, fakeClient := validTestConfig(t, okToRebootNode())

				firstCall := true

				expectedError := errors.New("Error node operation " + method)

				returnErrOnSecondCallF := func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					if firstCall {
						firstCall = false

						return true, node, nil
					}

					return true, nil, expectedError
				}

				fakeClient.PrependReactor(method, "nodes", returnErrOnSecondCallF)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})
		}

		t.Run("waiting_for_not_ok_to_reboot_annotation_fails_because", func(t *testing.T) {
			t.Parallel()

			t.Run("getting_Node_object_fails", func(t *testing.T) {
				t.Parallel()

				testConfig, node, fakeClient := validTestConfig(t, testNode())

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

				fakeClient.PrependReactor("get", "*", returnErrOnFailingCallF)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})

			t.Run("creating_Node_watcher_fails", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, okToRebootNode())

				expectedError := errors.New("creating watcher")
				f := func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
					return true, nil, expectedError
				}

				fakeClient.PrependWatchReactor("*", f)

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

						testConfig, _, fakeClient := validTestConfig(t, okToRebootNode())

						// Mock sending custom watch event.
						watcher := watch.NewFakeWithChanSize(1, true)
						testCase.watchEvent(watcher)
						fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

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

			testConfig, node, fakeClient := validTestConfig(t, nodeMadeUnschedulable())

			withOkToRebootFalseUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as schedulable, make error occur.
				return true, nil, expectedError
			}

			fakeClient.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("updating_Node_annotations_after_marking_Node_schedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, nodeMadeUnschedulable())

			withOkToRebootFalseUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if node.Annotations[constants.AnnotationAgentMadeUnschedulable] != constants.False {
					return true, node, nil
				}

				// If node is about to be annotated as no longer made unschedulable by agent, make error occur.
				return true, nil, expectedError
			}

			fakeClient.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("getting_Node_object_after_ok_to_reboot_is_given", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			callCounter := 0
			// 1. Updating info labels. TODO: Could be done with patch instead.
			// 2. Checking made unschedulable.
			// 3. Updating annotations and labels.
			// 4. Getting initial state while waiting for ok-to-reboot.
			// 5. Getting node object to mark it unschedulable etc.
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

			fakeClient.PrependReactor("get", "*", returnErrOnFailingCallF)

			err := getAgentRunningError(t, testConfig)
			if err == nil {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("setting_reboot_in_progress_annotation_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error setting reboot in progress annotation")

			fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if v, ok := node.Annotations[constants.AnnotationRebootInProgress]; ok && v == constants.True {
					// If node is about to be marked as reboot is in progress, make error occur.
					// For simplicity of logic, keep the happy path intended.
					return true, nil, expectedError
				}

				return true, node, nil
			})

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("marking_Node_unschedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as unschedulable")

			fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				node, _ := action.(k8stesting.UpdateActionImpl).GetObject().(*corev1.Node)

				if !node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as unschedulable, make error occur.
				return true, nil, expectedError
			})

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("getting_nodes_for_deletion_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error getting pods for deletion")

			fakeClient.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, expectedError
			})

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})
	})
}

func validTestConfig(t *testing.T, node *corev1.Node) (*agent.Config, *corev1.Node, *k8stesting.Fake) {
	t.Helper()

	files := map[string]string{
		"/usr/share/flatcar/update.conf": "GROUP=imageGroup",
		"/etc/flatcar/update.conf":       "GROUP=configuredGroup",
		"/etc/os-release":                "ID=testID\nVERSION=testVersion",
	}

	hostFilesPrefix := t.TempDir()

	createTestFiles(t, files, hostFilesPrefix)

	fakeClient := fake.NewSimpleClientset(node)

	return &agent.Config{
		Clientset:       fakeClient,
		StatusReceiver:  &mockStatusReceiver{},
		Rebooter:        &mockRebooter{},
		NodeName:        node.Name,
		HostFilesPrefix: hostFilesPrefix,
	}, node, &fakeClient.Fake
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

func withOkToRebootTrueUpdate(fakeClient *k8stesting.Fake, node *corev1.Node) {
	watcher := watch.NewFakeWithChanSize(1, true)
	updatedNode := node.DeepCopy()
	updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.True
	updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
	watcher.Modify(updatedNode)
	fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))
}

func withOkToRebootFalseUpdate(fakeClient *k8stesting.Fake, node *corev1.Node) {
	watcher := watch.NewFakeWithChanSize(1, true)
	updatedNode := node.DeepCopy()
	updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.False
	watcher.Modify(updatedNode)
	fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))
}
