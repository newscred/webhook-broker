package data

// Channel is the object that producer broadcasts to and consumer consumes from
type Channel struct {
	MessageStakeholder
	ChannelID string
}

// QuickFix fixes the model to set default ID, name same as channel id, created and updated at to current time.
func (channel *Channel) QuickFix() bool {
	madeChanges := channel.BasePaginateable.QuickFix()
	madeChanges = setValIfBothNotEmpty(&channel.Name, &channel.ChannelID) || madeChanges
	return madeChanges
}

// IsInValidState returns false if any of channel id or name or token is empty
func (channel *Channel) IsInValidState() bool {
	if len(channel.ChannelID) <= 0 || len(channel.Name) <= 0 || len(channel.Token) <= 0 {
		return false
	}
	return true
}

// NewChannel creates new Consumer
func NewChannel(channelID string, token string) (*Channel, error) {
	if len(channelID) <= 0 || len(token) <= 0 {
		return nil, ErrInsufficientInformationForCreating
	}
	channel := Channel{ChannelID: channelID, MessageStakeholder: createMessageStakeholder(channelID, token)}
	return &channel, nil
}
