package utils

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"

	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/types"
	uuid "github.com/satori/go.uuid"

	"github.com/sirupsen/logrus"
)

// LookupDatastoreConfig get a datastore config based on uuid
func LookupDatastoreConfig(sub pubsub.Subscription, dsID uuid.UUID) (*types.DatastoreConfig, error) {

	if dsID == nilUUID {
		err := fmt.Errorf("lookupDatastoreConfig(%s): No datastore ID", dsID.String())
		logrus.Errorln(err)
		return nil, err
	}
	cfg, err := sub.Get(dsID.String())
	if err != nil {
		err2 := fmt.Errorf("lookupDatastoreConfig(%s) error: %v",
			dsID.String(), err)
		logrus.Errorln(err2)
		return nil, err2
	}
	dst := cfg.(types.DatastoreConfig)
	return &dst, nil
}

var (
	goroutinePrefix = []byte("goroutine ")
)

func GetGoRoutineID() int {
	buf := make([]byte, 32)
	n := runtime.Stack(buf, false)
	buf = buf[:n]
	// goroutine 1 [running]: ...

	buf, ok := bytes.CutPrefix(buf, goroutinePrefix)
	if !ok {
		return 0
	}

	i := bytes.IndexByte(buf, ' ')
	if i < 0 {
		return 0
	}

	id, _ := strconv.Atoi(string(buf[:i]))
	return id
}
