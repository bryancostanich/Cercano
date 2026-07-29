package ui

import "cercano/source/server/pkg/agentclient"

type taskChangeMsg struct {
	kind string
	task *agentclient.TaskNode
}
