package data

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	sampleChannel             = getChannel()
	sampleCallbackURL         = getSampleURL("http://imytech.net/")
	sampleRelativeCallbackURL = getSampleURL("./")
	getSampleURL              = func(sampleURL string) *url.URL {
		url, _ := url.Parse(sampleURL)
		return url
	}
)

func getChannel() *Channel {
	channel, _ := NewChannel("testchannelforconsumer", "token")
	return channel
}

func TestNewConsumer(t *testing.T) {
	t.Run("EmptyID", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumer(sampleChannel, "", "", sampleCallbackURL, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("EmptyToken", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumer(sampleChannel, someID, "", sampleCallbackURL, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("NilChannel", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumer(nil, someID, someToken, sampleCallbackURL, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("RelativeURL", func(t *testing.T) {
		t.Parallel()
		_, err := NewConsumer(sampleChannel, someID, someToken, sampleRelativeCallbackURL, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		consumer, err := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		assert.Nil(t, err)
		assert.NotNil(t, consumer.ID)
		assert.Equal(t, someID, consumer.ConsumerID)
		assert.Equal(t, someID, consumer.Name)
		assert.Equal(t, someToken, consumer.Token)
	})

}

func TestNewConsumerWithVariousConsumerType(t *testing.T) {
	cases := []struct {
		name                 string
		typeString           string
		expectedConsumerType ConsumerType
	}{
		{name: "PushConsumer", typeString: "push", expectedConsumerType: PushConsumer},
		{name: "PullConsumer", typeString: "pull", expectedConsumerType: PullConsumer},
		{name: "EmptyConsumer", typeString: "", expectedConsumerType: PushConsumer},
		{name: "WrongConsumerType", typeString: "wrongType", expectedConsumerType: PushConsumer},
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			consumer, err := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, tt.typeString)

			if tt.typeString == "wrongType" {
				assert.NotNil(t, err)
				assert.Equal(t, "invalid consumerType: wrongType", err.Error())
				return
			}
			assert.Nil(t, err)
			assert.Nil(t, err)
			assert.NotNil(t, consumer.ID)
			assert.Equal(t, someID, consumer.ConsumerID)
			assert.Equal(t, someID, consumer.Name)
			assert.Equal(t, someToken, consumer.Token)
			assert.Equal(t, tt.expectedConsumerType, consumer.Type)
		})
	}
}

func TestConsumerIsInValidState(t *testing.T) {
	t.Run("True", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		assert.True(t, consumer.IsInValidState())
	})
	t.Run("EmptyIDFalse", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		consumer.ConsumerID = ""
		assert.False(t, consumer.IsInValidState())
	})
	t.Run("NilChannelFalse", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		consumer.ConsumingFrom = nil
		assert.False(t, consumer.IsInValidState())
	})
	t.Run("RelativeURLFalse", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		consumer.CallbackURL = sampleRelativeCallbackURL.String()
		assert.False(t, consumer.IsInValidState())
	})
}

func TestConsumerQuickFix(t *testing.T) {
	t.Run("ChildQuickFixChange", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		consumer.Name = ""
		assert.False(t, consumer.IsInValidState())
		assert.True(t, len(consumer.Name) <= 0)
		assert.True(t, consumer.QuickFix())
		assert.True(t, consumer.IsInValidState())
		assert.Equal(t, someID, consumer.Name)
	})
	t.Run("ParentOnlyQuickFixChange", func(t *testing.T) {
		t.Parallel()
		consumer, _ := NewConsumer(sampleChannel, someID, someToken, sampleCallbackURL, "")
		consumer.Name = someName
		assert.True(t, consumer.QuickFix())
		assert.Equal(t, someName, consumer.Name)
	})
}

func TestGetChannelIDSafely(t *testing.T) {
	consumer, _ := NewConsumer(sampleChannel, "consumer-someID", someToken, sampleCallbackURL, "")
	channel, _ := NewChannel(someID, someToken)
	channel.QuickFix()
	consumer.ConsumingFrom = channel
	assert.Equal(t, someID, consumer.GetChannelIDSafely())
}

func TestConsumerType_String(t *testing.T) {
	tests := []struct {
		name         string
		consumerType ConsumerType
		want         string
	}{
		{
			name:         "PullConsumer",
			consumerType: PullConsumer,
			want:         PullConsumerStr,
		},
		{
			name:         "PushConsumer",
			consumerType: PushConsumer,
			want:         PushConsumerStr,
		},
		{
			name:         "UnknownType",
			consumerType: ConsumerType(100), // An unknown type
			want:         PushConsumerStr,   // Should default to PushConsumerStr
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.consumerType.String(); got != tt.want {
				t.Errorf("ConsumerType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetConsumerTypeFromString(t *testing.T) {
	testCases := []struct {
		name                 string
		inputString          string
		expectedConsumerType ConsumerType
		expectedError        error
	}{
		{"PullConsumer", PullConsumerStr, PullConsumer, nil},
		{"PushConsumer", PushConsumerStr, PushConsumer, nil},
		{"EmptyConsumer", "", PushConsumer, nil},
		{"WrongConsumerType", "wrongType", PushConsumer, fmt.Errorf("invalid consumerType: wrongType")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			consumerType, err := getConsumerTypeFromString(testCase.inputString)

			assert.Equal(t, testCase.expectedConsumerType, consumerType)

			if testCase.expectedError != nil {
				assert.EqualError(t, err, testCase.expectedError.Error())
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
