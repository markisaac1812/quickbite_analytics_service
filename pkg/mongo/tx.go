package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// SupportsTransactions reports whether the server can run multi-document
// transactions: a replica-set member or a mongos. A standalone mongod cannot.
func SupportsTransactions(ctx context.Context, client *mongo.Client) (bool, error) {
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
	if err != nil {
		return false, fmt.Errorf("mongo hello: %w", err)
	}
	return hello.SetName != "" || hello.Msg == "isdbgrid", nil
}

// TxRunner runs a function inside one multi-document transaction.
//
// fn receives a ctx bound to the session: any driver call made with that ctx
// (in any repository) joins the transaction, so repositories need no
// transaction-specific code. The driver retries fn on transient errors
// (e.g. WriteConflict), so fn must not have side effects outside Mongo.
type TxRunner struct {
	client *mongo.Client
}

func NewTxRunner(client *mongo.Client) *TxRunner {
	return &TxRunner{client: client}
}

// InTx commits when fn returns nil and aborts when it returns an error, which
// is then returned unchanged (so sentinel errors survive errors.Is).
func (t *TxRunner) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	sess, err := t.client.StartSession()
	if err != nil {
		return fmt.Errorf("mongo start session: %w", err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))

	_, err = sess.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		return nil, fn(sc)
	})
	return err
}
