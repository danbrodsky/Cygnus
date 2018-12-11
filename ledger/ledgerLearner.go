package ledger

import (
	"net/rpc"
	"time"
)

type LedgerEntry struct {
	HostId string
	ClientId string
	StartTime time.Time
	EndTime time.Time
}

type LedgerLearner struct {
	nodeAddr string
}

type LedgerLearnerInterface interface {
	GetLedger() ([]LedgerEntry, error)
}

func Initialize(nodeAddr string) (l LedgerLearner) {
	return LedgerLearner{nodeAddr: nodeAddr}
}

func (l *LedgerLearner) GetLedger() ([]LedgerEntry, error) {
	con, err := rpc.Dial("tcp", l.nodeAddr)
	if err == nil {
		var reply []LedgerEntry
		err = con.Call("Ledger.ReadLedger", 0, &reply)
		if err == nil {
			return reply, nil
		} else {
			return make([]LedgerEntry, 0), err
		}
	} else {
		return make([]LedgerEntry, 0), err
	}
}
