package data

// MessageStakeholder represents all objects around a message, for example, Producer, Channel, Consumer
type MessageStakeholder struct {
	BasePaginateable
	Name  string
	Token string
}

// Producer represents generator of messages
type Producer struct {
	MessageStakeholder
	ProducerID string
}

// QuickFix fixes the model to set default ID, name same as producer id, created and updated at to current time.
func (prod *Producer) QuickFix() bool {
	madeChanges := prod.BasePaginateable.QuickFix()
	madeChanges = setValIfBothNotEmpty(&prod.Name, &prod.ProducerID) || madeChanges
	return madeChanges
}

// IsInValidState returns false if any of producer id or name or token is empty
func (prod *Producer) IsInValidState() bool {
	if len(prod.ProducerID) <= 0 || len(prod.Name) <= 0 || len(prod.Token) <= 0 {
		return false
	}
	return true
}

// NewProducer creates new Producer
func NewProducer(producerID string, token string) (*Producer, error) {
	if len(producerID) <= 0 || len(token) <= 0 {
		return nil, ErrInsufficientInformationForCreating
	}
	producer := Producer{ProducerID: producerID, MessageStakeholder: createMessageStakeholder(producerID, token)}
	return &producer, nil
}
