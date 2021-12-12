/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tests

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/hyperledger/fabric-protos-go/common"
	protopeer "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/common/ledger/testutil"
	"github.com/hyperledger/fabric/core/ledger"
	"github.com/hyperledger/fabric/core/ledger/kvledger"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/statedb/statecouchdb"
	"github.com/hyperledger/fabric/core/ledger/mock"
	"github.com/hyperledger/fabric/protoutil"
	"github.com/stretchr/testify/require"
)

// Test data used in the tests in this file was generated by v1.1 code https://gerrit.hyperledger.org/r/#/c/22749/6/core/ledger/kvledger/tests/v11_generate_test.go@22
// Folder, "testdata/v11/sample_ledgers" contains the data that was generated before commit hash feature was added.
// Folder, "testdata/v11/sample_ledgers_with_commit_hashes" contains the data that was generated after commit hash feature was added.

// TestV11 tests that a ledgersData folder created by v1.1 can be used with future releases after upgrading dbs.
// The test data was generated by v1.1 code https://github.com/hyperledger/fabric/blob/release-1.1/core/ledger/kvledger/tests/v11_generate_test.go#L22
func TestV11(t *testing.T) {
	env := newEnv(t)
	defer env.cleanup()

	ledgerFSRoot := env.initializer.Config.RootFSPath
	// pass false so that 'ledgersData' directory will not be created when unzipped to ledgerFSRoot
	require.NoError(t, testutil.Unzip("testdata/v11/sample_ledgers/ledgersData.zip", ledgerFSRoot, false))

	require.NoError(t, kvledger.UpgradeDBs(env.initializer.Config))
	// do not include bookkeeper and confighistory dbs since the v11 ledger doesn't have these dbs
	rebuildable := rebuildableStatedb | rebuildableHistoryDB | rebuildableBlockIndex
	env.verifyRebuilableDirEmpty(rebuildable)

	env.initLedgerMgmt()

	h1, h2 := env.newTestHelperOpenLgr("ledger1", t), env.newTestHelperOpenLgr("ledger2", t)
	dataHelper := &v1xSampleDataHelper{sampleDataVersion: "v1.1", t: t}

	dataHelper.verify(h1)
	dataHelper.verify(h2)

	// rebuild and verify again
	env.ledgerMgr.Close()
	require.NoError(t, kvledger.RebuildDBs(env.initializer.Config))
	env.verifyRebuilableDirEmpty(rebuildable)
	env.initLedgerMgmt()

	h1, h2 = env.newTestHelperOpenLgr("ledger1", t), env.newTestHelperOpenLgr("ledger2", t)
	dataHelper.verify(h1)
	dataHelper.verify(h2)

	h1.verifyCommitHashNotExists()
	h2.verifyCommitHashNotExists()
	h1.simulateDataTx("txid1_with_new_binary", func(s *simulator) {
		s.setState("cc1", "new_key", "new_value")
	})

	// add a new block and the new block should not contain a commit hash
	// because the previously committed block from 1.1 code did not contain commit hash
	h1.cutBlockAndCommitLegacy()
	h1.verifyCommitHashNotExists()
}

func TestV11CommitHashes(t *testing.T) {
	testCases := []struct {
		description               string
		v11SampleDataPath         string
		preResetCommitHashExists  bool
		resetFunc                 func(h *testhelper, ledgerFSRoot string)
		postResetCommitHashExists bool
	}{
		{
			"Reset (no existing CommitHash)",
			"testdata/v11/sample_ledgers/ledgersData.zip",
			false,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.ResetAllKVLedgers(ledgerFSRoot))
			},
			true,
		},

		{
			"Rollback to genesis block (no existing CommitHash)",
			"testdata/v11/sample_ledgers/ledgersData.zip",
			false,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.RollbackKVLedger(ledgerFSRoot, h.lgrid, 0))
			},
			true,
		},

		{
			"Rollback to block other than genesis block (no existing CommitHash)",
			"testdata/v11/sample_ledgers/ledgersData.zip",
			false,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.RollbackKVLedger(ledgerFSRoot, h.lgrid, h.currentHeight()/2+1))
			},
			false,
		},

		{
			"Reset (existing CommitHash)",
			"testdata/v11/sample_ledgers_with_commit_hashes/ledgersData.zip",
			true,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.ResetAllKVLedgers(ledgerFSRoot))
			},
			true,
		},

		{
			"Rollback to genesis block (existing CommitHash)",
			"testdata/v11/sample_ledgers_with_commit_hashes/ledgersData.zip",
			true,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.RollbackKVLedger(ledgerFSRoot, h.lgrid, 0))
			},
			true,
		},

		{
			"Rollback to block other than genesis block (existing CommitHash)",
			"testdata/v11/sample_ledgers_with_commit_hashes/ledgersData.zip",
			true,
			func(h *testhelper, ledgerFSRoot string) {
				require.NoError(t, kvledger.RollbackKVLedger(ledgerFSRoot, h.lgrid, h.currentHeight()/2+1))
			},
			true,
		},
	}

	for _, testCase := range testCases {
		t.Run(
			testCase.description,
			func(t *testing.T) {
				testV11CommitHashes(
					t,
					testCase.v11SampleDataPath,
					testCase.preResetCommitHashExists,
					testCase.resetFunc,
					testCase.postResetCommitHashExists,
				)
			})
	}
}

func testV11CommitHashes(t *testing.T,
	v11DataPath string,
	preResetCommitHashExists bool,
	resetFunc func(*testhelper, string),
	postResetCommitHashExists bool,
) {
	env := newEnv(t)
	defer env.cleanup()

	ledgerFSRoot := env.initializer.Config.RootFSPath
	// pass false so that 'ledgersData' directory will not be created when unzipped to ledgerFSRoot
	require.NoError(t, testutil.Unzip(v11DataPath, ledgerFSRoot, false))

	require.NoError(t, kvledger.UpgradeDBs(env.initializer.Config))
	// do not include bookkeeper and confighistory dbs since the v11 ledger doesn't have these dbs
	rebuildable := rebuildableStatedb | rebuildableHistoryDB | rebuildableBlockIndex
	env.verifyRebuilableDirEmpty(rebuildable)

	env.initLedgerMgmt()
	h := env.newTestHelperOpenLgr("ledger1", t)
	blocksAndPvtData := h.retrieveCommittedBlocksAndPvtdata(0, h.currentHeight()-1)

	var commitHashPreReset []byte
	if preResetCommitHashExists {
		commitHashPreReset = h.currentCommitHash()
		h.verifyCommitHashExists()
	} else {
		h.verifyCommitHashNotExists()
	}

	env.closeLedgerMgmt()
	resetFunc(h, ledgerFSRoot)
	env.initLedgerMgmt()

	h = env.newTestHelperOpenLgr("ledger1", t)
	for i := int(h.currentHeight()); i < len(blocksAndPvtData); i++ {
		d := blocksAndPvtData[i]
		// add metadata slot for commit hash, as this would have be missing in the blocks from 1.1 prior to this feature
		for len(d.Block.Metadata.Metadata) < int(common.BlockMetadataIndex_COMMIT_HASH)+1 {
			d.Block.Metadata.Metadata = append(d.Block.Metadata.Metadata, []byte{})
		}
		// set previous block hash, as this is not present in the test blocks from 1.1
		d.Block.Header.PreviousHash = protoutil.BlockHeaderHash(blocksAndPvtData[i-1].Block.Header)
		require.NoError(t, h.lgr.CommitLegacy(d, &ledger.CommitOptions{FetchPvtDataFromLedger: true}))
	}

	if preResetCommitHashExists {
		commitHashPostReset := h.currentCommitHash()
		require.Equal(t, commitHashPreReset, commitHashPostReset)
	}
	if postResetCommitHashExists {
		h.verifyCommitHashExists()
	} else {
		h.verifyCommitHashNotExists()
	}

	bcInfo, err := h.lgr.GetBlockchainInfo()
	require.NoError(t, err)
	h.committer.blkgen.lastNum = bcInfo.Height - 1
	h.committer.blkgen.lastHash = bcInfo.CurrentBlockHash

	h.simulateDataTx("txid1_with_new_binary", func(s *simulator) {
		s.setState("cc1", "new_key", "new_value")
	})
	h.cutBlockAndCommitLegacy()

	if postResetCommitHashExists {
		h.verifyCommitHashExists()
	} else {
		h.verifyCommitHashNotExists()
	}
}

// TestV13WithStateCouchdb tests that a ledgersData folder and couchdb data created by v1.3 can be read by latest fabric version after upgrading dbs.
// The test data was generated by v1.3 code https://gerrit.hyperledger.org/r/#/c/fabric/+/34078/3/core/ledger/kvledger/tests/v13_generate_test.go@60
func TestV13WithStateCouchdb(t *testing.T) {
	env := newEnv(t)
	defer env.cleanup()

	ledgerFSRoot := env.initializer.Config.RootFSPath
	// pass false so that 'ledgersData' directory will not be created when unzipped to ledgerFSRoot
	require.NoError(t, testutil.Unzip("testdata/v13_statecouchdb/sample_ledgers/ledgersData.zip", ledgerFSRoot, false))

	couchdbConfig, cleanup := startCouchDBWithV13Data(t, ledgerFSRoot)
	defer cleanup()
	env.initializer.Config.StateDBConfig.StateDatabase = "CouchDB"
	env.initializer.Config.StateDBConfig.CouchDB = couchdbConfig
	env.initializer.HealthCheckRegistry = &mock.HealthCheckRegistry{}
	env.initializer.ChaincodeLifecycleEventProvider = &mock.ChaincodeLifecycleEventProvider{}

	require.NoError(t, kvledger.UpgradeDBs(env.initializer.Config))
	require.True(t, statecouchdb.IsEmpty(t, couchdbConfig))
	rebuildable := rebuildableBookkeeper | rebuildableConfigHistory | rebuildableHistoryDB | rebuildableBlockIndex
	env.verifyRebuilableDirEmpty(rebuildable)

	env.initLedgerMgmt()

	h1, h2 := env.newTestHelperOpenLgr("ledger1", t), env.newTestHelperOpenLgr("ledger2", t)
	dataHelper := &v1xSampleDataHelper{sampleDataVersion: "v1.3", t: t}
	dataHelper.verify(h1)
	dataHelper.verify(h2)

	// rebuild and verify again
	env.ledgerMgr.Close()
	require.NoError(t, kvledger.RebuildDBs(env.initializer.Config))
	require.True(t, statecouchdb.IsEmpty(t, couchdbConfig))
	env.verifyRebuilableDirEmpty(rebuildable)
	env.initLedgerMgmt()

	h1, h2 = env.newTestHelperOpenLgr("ledger1", t), env.newTestHelperOpenLgr("ledger2", t)
	dataHelper.verify(h1)
	dataHelper.verify(h2)
}

// TestInitLedgerPanicWithV11Data tests init ledger panic cases caused by ledger dbs in old formats.
// It tests stateleveldb.
func TestInitLedgerPanicWithV11Data(t *testing.T) {
	env := newEnv(t)
	defer env.cleanup()

	ledgerFSRoot := env.initializer.Config.RootFSPath
	require.NoError(t, testutil.Unzip("testdata/v11/sample_ledgers/ledgersData.zip", ledgerFSRoot, false))
	testInitLedgerPanic(t, env, ledgerFSRoot, nil)
}

// TestInitLedgerPanicWithV13Data tests init ledger panic cases caused by ledger dbs in old formats.
// It tests statecouchdb.
func TestInitLedgerPanicWithV13Data(t *testing.T) {
	env := newEnv(t)
	defer env.cleanup()

	ledgerFSRoot := env.initializer.Config.RootFSPath
	// pass false so that 'ledgersData' directory will not be created when unzipped to ledgerFSRoot
	require.NoError(t, testutil.Unzip("testdata/v13_statecouchdb/sample_ledgers/ledgersData.zip", ledgerFSRoot, false))

	couchdbConfig, cleanup := startCouchDBWithV13Data(t, ledgerFSRoot)
	defer cleanup()
	env.initializer.Config.StateDBConfig.StateDatabase = "CouchDB"
	env.initializer.Config.StateDBConfig.CouchDB = couchdbConfig
	env.initializer.HealthCheckRegistry = &mock.HealthCheckRegistry{}
	env.initializer.ChaincodeLifecycleEventProvider = &mock.ChaincodeLifecycleEventProvider{}
	testInitLedgerPanic(t, env, ledgerFSRoot, couchdbConfig)
}

// Verify init ledger panic due to old DB formats. Drop each DB after panic so that we can test panic for next DB.
func testInitLedgerPanic(t *testing.T, env *env, ledgerFSRoot string, couchdbConfig *ledger.CouchDBConfig) {
	t.Logf("verifying that a panic occurs because idStore has old format and then reformat the idstore to proceed")
	idStorePath := kvledger.LedgerProviderPath(ledgerFSRoot)
	require.PanicsWithValue(
		t,
		fmt.Sprintf("Error in instantiating ledger provider: unexpected format. db info = [leveldb for channel-IDs at [%s]], data format = [], expected format = [2.0]",
			idStorePath),
		func() { env.initLedgerMgmt() },
		"A panic should occur because idstore is in format 1.x",
	)
	kvledger.UpgradeIDStoreFormat(t, ledgerFSRoot)

	t.Logf("verifying that a panic occurs because blockstore index has old format and then drop the idstore to proceed")
	blkIndexPath := path.Join(kvledger.BlockStorePath(ledgerFSRoot), "index")
	require.PanicsWithValue(
		t,
		fmt.Sprintf("Error in instantiating ledger provider: unexpected format. db info = [leveldb at [%s]], data format = [], expected format = [2.0]",
			blkIndexPath),
		func() { env.initLedgerMgmt() },
		"A panic should occur because block store index is in format 1.x",
	)
	require.NoError(t, os.RemoveAll(blkIndexPath))

	t.Logf("verifying that a panic occurs because historydb has old format and then drop the historydb to proceed")
	historyDBPath := kvledger.HistoryDBPath(ledgerFSRoot)
	require.PanicsWithValue(
		t,
		fmt.Sprintf("Error in instantiating ledger provider: unexpected format. db info = [leveldb at [%s]], data format = [], expected format = [2.0]",
			historyDBPath),
		func() { env.initLedgerMgmt() },
		"A panic should occur because history is in format 1.x",
	)
	require.NoError(t, os.RemoveAll(historyDBPath))

	if couchdbConfig == nil {
		t.Logf("verifying that a panic occurs because stateleveldb has old format and then drop the statedb to proceed")
		stateLevelDBPath := kvledger.StateDBPath(ledgerFSRoot)
		require.PanicsWithValue(
			t,
			fmt.Sprintf(
				"Error in instantiating ledger provider: unexpected format. db info = [leveldb at [%s]], data format = [], expected format = [2.0]",
				stateLevelDBPath,
			),
			func() { env.initLedgerMgmt() },
			"A panic should occur because statedb is in format 1.x",
		)
		require.NoError(t, os.RemoveAll(stateLevelDBPath))
	} else {
		t.Logf("verifying that a panic occurs because statecouchdb has old format and then drop the statedb to proceed")
		require.PanicsWithValue(
			t,
			"Error in instantiating ledger provider: unexpected format. db info = [CouchDB for state database], data format = [], expected format = [2.0]",
			func() { env.initLedgerMgmt() },
			"A panic should occur because statedb is in format 1.x",
		)
		require.NoError(t, statecouchdb.DropApplicationDBs(couchdbConfig))
	}
}

func startCouchDBWithV13Data(t *testing.T, ledgerFSRoot string) (*ledger.CouchDBConfig, func()) {
	// unzip couchdb data to prepare the mount dir
	couchdbDataUnzipDir := filepath.Join(ledgerFSRoot, "couchdbData")
	require.NoError(t, os.Mkdir(couchdbDataUnzipDir, os.ModePerm))
	require.NoError(t, testutil.Unzip("testdata/v13_statecouchdb/sample_ledgers/couchdbData.zip", couchdbDataUnzipDir, false))

	// prepare the local.d mount dir to overwrite the number of shards and nodes so that they match the couchdb data generated from v1.3
	localdHostDir := filepath.Join(ledgerFSRoot, "local.d")
	require.NoError(t, os.MkdirAll(localdHostDir, os.ModePerm))
	testutil.CopyDir("testdata/v13_statecouchdb/couchdb_etc/local.d", localdHostDir, true)

	// start couchdb using couchdbDataUnzipDir and localdHostDir as mount dirs
	couchdbBinds := []string{
		fmt.Sprintf("%s:%s", couchdbDataUnzipDir, "/opt/couchdb/data"),
		fmt.Sprintf("%s:%s", localdHostDir, "/opt/couchdb/etc/local.d"),
	}
	couchAddress, cleanup := statecouchdb.StartCouchDB(t, couchdbBinds)

	// set required config data to use state couchdb
	couchdbConfig := &ledger.CouchDBConfig{
		Address:             couchAddress,
		Username:            "admin",
		Password:            "adminpw",
		MaxRetries:          3,
		MaxRetriesOnStartup: 3,
		RequestTimeout:      10 * time.Second,
		RedoLogPath:         filepath.Join(ledgerFSRoot, "couchdbRedoLogs"),
	}

	return couchdbConfig, cleanup
}

// v1xSampleDataHelper provides a set of functions to verify the ledger (after upgraded to latest data format).
// It verifies the ledger under the assumption that the ledger was generated by the specific generation code from v1.1 or v1.3.
// For v1.1, the sample ledger data was generated by https://github.com/hyperledger/fabric/blob/release-1.1/core/ledger/kvledger/tests/v11_generate_test.go#L22
// This generate function constructed two ledgers and populateed the ledgers using this code
// (https://github.com/hyperledger/fabric/blob/release-1.1/core/ledger/kvledger/tests/sample_data_helper.go#L55)
// For v1.3, the sample ledger data was generated by CR (https://gerrit.hyperledger.org/r/#/c/fabric/+/34078/3/core/ledger/kvledger/tests/v13_generate_test.go@60).
// This generate function constructed two ledgers and populated the ledgers using this code
// (https://github.com/hyperledger/fabric/blob/release-1.3/core/ledger/kvledger/tests/sample_data_helper.go#L55)
type v1xSampleDataHelper struct {
	sampleDataVersion string
	t                 *testing.T
}

func (d *v1xSampleDataHelper) verify(h *testhelper) {
	d.verifyState(h)
	d.verifyBlockAndPvtdata(h)
	d.verifyGetTransactionByID(h)
	d.verifyConfigHistory(h)
	d.verifyHistory(h)
}

func (d *v1xSampleDataHelper) verifyState(h *testhelper) {
	lgrid := h.lgrid
	h.verifyPubState("cc1", "key1", d.sampleVal("value13", lgrid))
	h.verifyPubState("cc1", "key2", "")
	h.verifyPvtState("cc1", "coll1", "key3", d.sampleVal("value14", lgrid))
	h.verifyPvtState("cc1", "coll1", "key4", "")
	h.verifyPvtState("cc1", "coll2", "key3", d.sampleVal("value09", lgrid))
	h.verifyPvtState("cc1", "coll2", "key4", d.sampleVal("value10", lgrid))

	h.verifyPubState("cc2", "key1", d.sampleVal("value03", lgrid))
	h.verifyPubState("cc2", "key2", d.sampleVal("value04", lgrid))
	h.verifyPvtState("cc2", "coll1", "key3", d.sampleVal("value07", lgrid))
	h.verifyPvtState("cc2", "coll1", "key4", d.sampleVal("value08", lgrid))
	h.verifyPvtState("cc2", "coll2", "key3", d.sampleVal("value11", lgrid))
	h.verifyPvtState("cc2", "coll2", "key4", d.sampleVal("value12", lgrid))
}

func (d *v1xSampleDataHelper) verifyHistory(h *testhelper) {
	lgrid := h.lgrid
	expectedHistoryCC1Key1 := []string{
		d.sampleVal("value13", lgrid),
		d.sampleVal("value01", lgrid),
	}
	h.verifyHistory("cc1", "key1", expectedHistoryCC1Key1)
}

func (d *v1xSampleDataHelper) verifyConfigHistory(h *testhelper) {
	lgrid := h.lgrid
	h.verifyMostRecentCollectionConfigBelow(10, "cc1",
		&expectedCollConfInfo{5, d.sampleCollConf2(lgrid, "cc1")})

	h.verifyMostRecentCollectionConfigBelow(5, "cc1",
		&expectedCollConfInfo{3, d.sampleCollConf1(lgrid, "cc1")})

	h.verifyMostRecentCollectionConfigBelow(10, "cc2",
		&expectedCollConfInfo{5, d.sampleCollConf2(lgrid, "cc2")})

	h.verifyMostRecentCollectionConfigBelow(5, "cc2",
		&expectedCollConfInfo{3, d.sampleCollConf1(lgrid, "cc2")})
}

func (d *v1xSampleDataHelper) verifyConfigHistoryDoesNotExist(h *testhelper) {
	h.verifyMostRecentCollectionConfigBelow(10, "cc1", nil)
	h.verifyMostRecentCollectionConfigBelow(10, "cc2", nil)
}

func (d *v1xSampleDataHelper) verifyBlockAndPvtdata(h *testhelper) {
	lgrid := h.lgrid
	h.verifyBlockAndPvtData(2, nil, func(r *retrievedBlockAndPvtdata) {
		r.hasNumTx(2)
		r.hasNoPvtdata()
	})

	h.verifyBlockAndPvtData(4, nil, func(r *retrievedBlockAndPvtdata) {
		r.hasNumTx(2)
		r.pvtdataShouldContain(0, "cc1", "coll1", "key3", d.sampleVal("value05", lgrid))
		r.pvtdataShouldContain(1, "cc2", "coll1", "key3", d.sampleVal("value07", lgrid))
	})
}

func (d *v1xSampleDataHelper) verifyGetTransactionByID(h *testhelper) {
	h.verifyTxValidationCode("txid7", protopeer.TxValidationCode_VALID)
	h.verifyTxValidationCode("txid8", protopeer.TxValidationCode_MVCC_READ_CONFLICT)
}

func (d *v1xSampleDataHelper) sampleVal(val, ledgerid string) string {
	return fmt.Sprintf("%s:%s", val, ledgerid)
}

func (d *v1xSampleDataHelper) sampleCollConf1(ledgerid, ccName string) []*collConf {
	switch d.sampleDataVersion {
	case "v1.1":
		return []*collConf{
			{name: "coll1", members: []string{"org1", "org2"}},
			{name: ledgerid, members: []string{"org1", "org2"}},
			{name: ccName, members: []string{"org1", "org2"}},
		}
	case "v1.3":
		return []*collConf{
			{name: "coll1"},
			{name: ledgerid},
			{name: ccName},
		}
	default:
		// should not happen
		require.Failf(d.t, "sample data version %s is wrong", d.sampleDataVersion)
		return nil
	}
}

func (d *v1xSampleDataHelper) sampleCollConf2(ledgerid string, ccName string) []*collConf {
	switch d.sampleDataVersion {
	case "v1.1":
		return []*collConf{
			{name: "coll1", members: []string{"org1", "org2"}},
			{name: "coll2", members: []string{"org1", "org2"}},
			{name: ledgerid, members: []string{"org1", "org2"}},
			{name: ccName, members: []string{"org1", "org2"}},
		}
	case "v1.3":
		return []*collConf{
			{name: "coll1"},
			{name: "coll2"},
			{name: ledgerid},
			{name: ccName},
		}
	default:
		// should not happen
		require.Failf(d.t, "sample data version %s is wrong", d.sampleDataVersion)
		return nil
	}
}