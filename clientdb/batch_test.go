package clientdb

import (
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcutil"
	"github.com/lightninglabs/agora/client/account"
	"github.com/lightninglabs/agora/client/clmscript"
	"github.com/lightninglabs/agora/client/order"
)

var (
	testBatchID = order.BatchID{0x01, 0x02, 0x03}

	testCases = []struct {
		name        string
		expectedErr string
		runTest     func(db *DB, a *order.Ask, b *order.Bid,
			acct *account.Account) error
	}{
		{
			name:        "len mismatch order",
			expectedErr: "order modifier length mismatch",
			runTest: func(db *DB, a *order.Ask, _ *order.Bid,
				_ *account.Account) error {

				return db.StorePendingBatch(
					testBatchID, []order.Nonce{a.Nonce()}, nil,
					nil, nil,
				)
			},
		},
		{
			name:        "len mismatch account",
			expectedErr: "account modifier length mismatch",
			runTest: func(db *DB, a *order.Ask, _ *order.Bid,
				acct *account.Account) error {

				return db.StorePendingBatch(
					testBatchID, nil, nil,
					[]*account.Account{acct}, nil,
				)
			},
		},
		{
			name:        "non-existent order",
			expectedErr: ErrNoOrder.Error(),
			runTest: func(db *DB, a *order.Ask, _ *order.Bid,
				acct *account.Account) error {

				modifiers := [][]order.Modifier{{
					order.StateModifier(order.StateExecuted),
				}}
				return db.StorePendingBatch(
					testBatchID, []order.Nonce{{0, 1, 2}},
					modifiers, nil, nil,
				)
			},
		},
		{
			name:        "non-existent account",
			expectedErr: ErrAccountNotFound.Error(),
			runTest: func(db *DB, a *order.Ask, _ *order.Bid,
				acct *account.Account) error {

				acct.TraderKey.PubKey = clmscript.IncrementKey(
					acct.TraderKey.PubKey,
				)
				modifiers := [][]account.Modifier{{
					account.StateModifier(account.StateClosed),
				}}
				return db.StorePendingBatch(
					testBatchID, nil, nil,
					[]*account.Account{acct}, modifiers,
				)
			},
		},
		{
			name:        "no pending batch",
			expectedErr: order.ErrNoPendingBatch.Error(),
			runTest: func(db *DB, a *order.Ask, b *order.Bid,
				acct *account.Account) error {

				_, err := db.PendingBatchID()
				return err
			},
		},
		{
			name:        "mark batch complete without pending",
			expectedErr: order.ErrNoPendingBatch.Error(),
			runTest: func(db *DB, a *order.Ask, b *order.Bid,
				acct *account.Account) error {

				return db.MarkBatchComplete(testBatchID)
			},
		},
		{
			name:        "mark batch complete mismatch",
			expectedErr: "batch id mismatch",
			runTest: func(db *DB, a *order.Ask, b *order.Bid,
				acct *account.Account) error {

				err := db.StorePendingBatch(
					testBatchID, nil, nil, nil, nil,
				)
				if err != nil {
					return err
				}

				wrongBatchID := testBatchID
				wrongBatchID[0] ^= 1
				return db.MarkBatchComplete(wrongBatchID)
			},
		},
		{
			name:        "happy path",
			expectedErr: "",
			runTest: func(db *DB, a *order.Ask, b *order.Bid,
				acct *account.Account) error {

				// Store some changes to the orders and account.
				orders := []order.Order{a, b}
				orderNonces := []order.Nonce{a.Nonce(), b.Nonce()}
				orderModifiers := [][]order.Modifier{
					{order.UnitsFulfilledModifier(42)},
					{order.UnitsFulfilledModifier(21)},
				}
				accounts := []*account.Account{acct}
				acctModifiers := [][]account.Modifier{{
					account.StateModifier(
						account.StatePendingOpen,
					),
				}}
				err := db.StorePendingBatch(
					testBatchID, orderNonces, orderModifiers,
					accounts, acctModifiers,
				)
				if err != nil {
					return err
				}

				// The pending batch ID should reflect
				// correctly.
				dbBatchID, err := db.PendingBatchID()
				if err != nil {
					return err
				}
				if dbBatchID != testBatchID {
					return fmt.Errorf("expected pending "+
						"batch id %x, got %x",
						testBatchID, dbBatchID)
				}

				// Verify the updates have not been applied to
				// disk yet.
				err = checkUpdate(
					db, a.Nonce(), b.Nonce(),
					a.Details().UnitsUnfulfilled,
					b.Details().UnitsUnfulfilled,
					acct.TraderKey.PubKey, acct.State,
				)
				if err != nil {
					return err
				}

				// Mark the batch as complete.
				err = db.MarkBatchComplete(testBatchID)
				if err != nil {
					return err
				}

				// Verify the updates have been applied to disk
				// properly.
				for i, a := range accounts {
					for _, modifier := range acctModifiers[i] {
						modifier(a)
					}
				}
				for i, o := range orders {
					for _, modifier := range orderModifiers[i] {
						modifier(o.Details())
					}
				}
				return checkUpdate(
					db, a.Nonce(), b.Nonce(),
					a.Details().UnitsUnfulfilled,
					b.Details().UnitsUnfulfilled,
					acct.TraderKey.PubKey, acct.State,
				)
			},
		},
		{
			name:        "overwrite pending batch",
			expectedErr: "",
			runTest: func(db *DB, a *order.Ask, b *order.Bid,
				acct *account.Account) error {

				// First, we'll store a version of the batch
				// that updates all order and accounts.
				orderModifier := order.UnitsFulfilledModifier(42)
				err := db.StorePendingBatch(
					testBatchID,
					[]order.Nonce{a.Nonce(), b.Nonce()},
					[][]order.Modifier{
						{orderModifier}, {orderModifier},
					},
					[]*account.Account{acct},
					[][]account.Modifier{{account.StateModifier(
						account.StatePendingUpdate,
					)}},
				)
				if err != nil {
					return err
				}

				// Then, we'll assume the batch was overwritten,
				// and now only the ask order is part of it.
				err = db.StorePendingBatch(
					testBatchID,
					[]order.Nonce{a.Nonce()},
					[][]order.Modifier{{orderModifier}},
					nil, nil,
				)
				if err != nil {
					return err
				}

				// Mark the batch as complete. We should only
				// see the update for our ask order applied, but
				// not the rest.
				err = db.MarkBatchComplete(testBatchID)
				if err != nil {
					return err
				}

				return checkUpdate(
					db, a.Nonce(), b.Nonce(), 42,
					b.UnitsUnfulfilled,
					acct.TraderKey.PubKey, acct.State,
				)
			},
		},
	}
)

// checkUpdate is a helper closure we'll use to check whether the account and
// order updates of a batch have been applied.
func checkUpdate(db *DB, askNonce, bidNonce order.Nonce,
	askUnitsUnfulfilled, bidUnitsUnfulfilled order.SupplyUnit,
	accountKey *btcec.PublicKey, accountState account.State) error {

	o1, err := db.GetOrder(askNonce)
	if err != nil {
		return err
	}
	if o1.Details().UnitsUnfulfilled != askUnitsUnfulfilled {
		return fmt.Errorf("unexpected number of unfulfilled "+
			"units, got %d wanted %d",
			o1.Details().UnitsUnfulfilled, askUnitsUnfulfilled)
	}

	o2, err := db.GetOrder(bidNonce)
	if err != nil {
		return err
	}
	if o2.Details().UnitsUnfulfilled != bidUnitsUnfulfilled {
		return fmt.Errorf("unexpected number of unfulfilled "+
			"units, got %d "+"wanted %d",
			o2.Details().UnitsUnfulfilled, bidUnitsUnfulfilled)
	}

	a2, err := db.Account(accountKey)
	if err != nil {
		return err
	}
	if a2.State != accountState {
		return fmt.Errorf("unexpected state of account, got "+
			"%v wanted %v", a2.State, accountState)
	}

	return nil
}

// TestPersistBatchResult tests that a batch result can be persisted correctly.
func TestPersistBatchResult(t *testing.T) {
	t.Parallel()

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Create a new store every time to make sure we start
			// with a clean slate.
			store, cleanup := newTestDB(t)
			defer cleanup()

			// Create a test account and two matching orders that
			// spend from that account. This never happens in real
			// life but is good enough to just test the database.
			acct := &account.Account{
				Value:         btcutil.SatoshiPerBitcoin,
				Expiry:        1337,
				TraderKey:     testTraderKeyDesc,
				AuctioneerKey: testAuctioneerKey,
				BatchKey:      testBatchKey,
				Secret:        sharedSecret,
				State:         account.StateOpen,
				HeightHint:    1,
			}
			ask := &order.Ask{
				Kit:         *dummyOrder(t, 900000),
				MaxDuration: 1337,
			}
			ask.State = order.StateSubmitted
			bid := &order.Bid{
				Kit:         *dummyOrder(t, 900000),
				MinDuration: 1337,
			}
			bid.State = order.StateSubmitted

			// Prepare the DB state by storing our test account and
			// orders.
			err := store.AddAccount(acct)
			if err != nil {
				t.Fatalf("error storing test account: %v", err)
			}
			err = store.SubmitOrder(ask)
			if err != nil {
				t.Fatalf("error storing test ask: %v", err)
			}
			err = store.SubmitOrder(bid)
			if err != nil {
				t.Fatalf("error storing test bid: %v", err)
			}

			// Run the test case and verify the result.
			err = tc.runTest(store, ask, bid, acct)
			switch {
			case err == nil && tc.expectedErr != "":
			case err != nil && tc.expectedErr != "":
				if strings.Contains(err.Error(), tc.expectedErr) {
					return
				}
			case err != nil && tc.expectedErr == "":
			default:
				return
			}

			t.Fatalf("unexpected error '%s', expected '%s'",
				err.Error(), tc.expectedErr)
		})
	}
}