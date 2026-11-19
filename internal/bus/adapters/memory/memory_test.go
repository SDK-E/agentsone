// SPDX-License-Identifier: MIT

package memory_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/bus/adapters/memory"
	"github.com/SDK-E/agentsone/internal/bus/bustest"
	"github.com/SDK-E/agentsone/internal/bus/domain"
)

func TestChannelsRepository(t *testing.T) {
	bustest.RunChannelContract(t, func(*testing.T) domain.ChannelRepository { return memory.NewChannels() })
}

func TestMessagesRepository(t *testing.T) {
	bustest.RunMessageContract(t, func(*testing.T) domain.MessageRepository { return memory.NewMessages() })
}
