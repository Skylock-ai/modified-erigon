// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package tracers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ledgerwatch/erigon-lib/kv/memdb"
	"github.com/ledgerwatch/erigon/common"
	"github.com/ledgerwatch/erigon/common/hexutil"
	"github.com/ledgerwatch/erigon/common/math"
	"github.com/ledgerwatch/erigon/core"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/core/vm"
	"github.com/ledgerwatch/erigon/core/vm/evmtypes"
	"github.com/ledgerwatch/erigon/crypto"
	"github.com/ledgerwatch/erigon/params"
	"github.com/ledgerwatch/erigon/rlp"
	"github.com/ledgerwatch/erigon/tests"
	"github.com/stretchr/testify/require"

	"github.com/holiman/uint256"
)

// To generate a new callTracer test, copy paste the makeTest method below into
// a Geth console and call it with a transaction hash you which to export.

/*
// makeTest generates a callTracer test by running a prestate reassembled and a
// call trace run, assembling all the gathered information into a test case.
var makeTest = function(tx, rewind) {
  // Generate the genesis block from the block, transaction and prestate data
  var block   = eth.getBlock(eth.getTransaction(tx).blockHash);
  var genesis = eth.getBlock(block.parentHash);

  delete genesis.gasUsed;
  delete genesis.logsBloom;
  delete genesis.parentHash;
  delete genesis.receiptsRoot;
  delete genesis.sha3Uncles;
  delete genesis.size;
  delete genesis.transactions;
  delete genesis.transactionsRoot;
  delete genesis.uncles;

  genesis.gasLimit  = genesis.gasLimit.toString();
  genesis.number    = genesis.number.toString();
  genesis.timestamp = genesis.timestamp.toString();

  genesis.alloc = debug.traceTransaction(tx, {tracer: "prestateTracer", rewind: rewind});
  for (var key in genesis.alloc) {
    genesis.alloc[key].nonce = genesis.alloc[key].nonce.toString();
  }
  genesis.config = admin.nodeInfo.protocols.eth.config;

  // Generate the call trace and produce the test input
  var result = debug.traceTransaction(tx, {tracer: "callTracer", rewind: rewind});
  delete result.time;

  console.log(JSON.stringify({
    genesis: genesis,
    context: {
      number:     block.number.toString(),
      difficulty: block.difficulty,
      timestamp:  block.timestamp.toString(),
      gasLimit:   block.gasLimit.toString(),
      miner:      block.miner,
    },
    input:  eth.getRawTransaction(tx),
    result: result,
  }, null, 2));
}
*/

// callTrace is the result of a callTracer run.
type callTrace struct {
	Type    string          `json:"type"`
	From    common.Address  `json:"from"`
	To      common.Address  `json:"to"`
	Input   hexutil.Bytes   `json:"input"`
	Output  hexutil.Bytes   `json:"output"`
	Gas     *hexutil.Uint64 `json:"gas,omitempty"`
	GasUsed *hexutil.Uint64 `json:"gasUsed,omitempty"`
	Value   *hexutil.Big    `json:"value,omitempty"`
	Error   string          `json:"error,omitempty"`
	Calls   []callTrace     `json:"calls,omitempty"`
}

type callContext struct {
	Number     math.HexOrDecimal64   `json:"number"`
	Difficulty *math.HexOrDecimal256 `json:"difficulty"`
	Time       math.HexOrDecimal64   `json:"timestamp"`
	GasLimit   math.HexOrDecimal64   `json:"gasLimit"`
	Miner      common.Address        `json:"miner"`
}

// callTracerTest defines a single test to check the call tracer against.
type callTracerTest struct {
	Genesis *core.Genesis `json:"genesis"`
	Context *callContext  `json:"context"`
	Input   string        `json:"input"`
	Result  *callTrace    `json:"result"`
}

func TestPrestateTracerCreate2(t *testing.T) {
	unsignedTx := types.NewTransaction(1, common.HexToAddress("0x00000000000000000000000000000000deadbeef"),
		uint256.NewInt(0), 5000000, uint256.NewInt(1), []byte{})

	privateKeyECDSA, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	signer := types.LatestSignerForChainID(big.NewInt(1))
	txn, err := types.SignTx(unsignedTx, *signer, privateKeyECDSA)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	/**
		This comes from one of the test-vectors on the Skinny Create2 - EIP

	    address 0x00000000000000000000000000000000deadbeef
	    salt 0x00000000000000000000000000000000000000000000000000000000cafebabe
	    init_code 0xdeadbeef
	    gas (assuming no mem expansion): 32006
	    result: 0x60f3f640a8508fC6a86d45DF051962668E1e8AC7
	*/
	origin, _ := signer.Sender(txn)
	txContext := evmtypes.TxContext{
		Origin:   origin,
		GasPrice: uint256.NewInt(1),
	}
	context := evmtypes.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		Coinbase:    common.Address{},
		BlockNumber: 8000000,
		Time:        5,
		Difficulty:  big.NewInt(0x30000),
		GasLimit:    uint64(6000000),
	}
	alloc := core.GenesisAlloc{}

	// The code pushes 'deadbeef' into memory, then the other params, and calls CREATE2, then returns
	// the address
	alloc[common.HexToAddress("0x00000000000000000000000000000000deadbeef")] = core.GenesisAccount{
		Nonce:   1,
		Code:    hexutil.MustDecode("0x63deadbeef60005263cafebabe6004601c6000F560005260206000F3"),
		Balance: big.NewInt(1),
	}
	alloc[origin] = core.GenesisAccount{
		Nonce:   1,
		Code:    []byte{},
		Balance: big.NewInt(500000000000000),
	}

	_, tx := memdb.NewTestTx(t)
	rules := &params.Rules{}
	statedb, _ := tests.MakePreState(rules, tx, alloc, context.BlockNumber)

	// Create the tracer, the EVM environment and run it
	tracer, err := New("prestateTracer", new(Context))
	if err != nil {
		t.Fatalf("failed to create call tracer: %v", err)
	}
	evm := vm.NewEVM(context, txContext, statedb, params.MainnetChainConfig, vm.Config{Debug: true, Tracer: tracer})

	msg, err := txn.AsMessage(*signer, nil, rules)
	if err != nil {
		t.Fatalf("failed to prepare transaction for tracing: %v", err)
	}
	st := core.NewStateTransition(evm, msg, new(core.GasPool).AddGas(txn.GetGas()))
	if _, err = st.TransitionDb(false, false); err != nil {
		t.Fatalf("failed to execute transaction: %v", err)
	}
	// Retrieve the trace result and compare against the etalon
	res, err := tracer.GetResult()
	if err != nil {
		t.Fatalf("failed to retrieve trace result: %v", err)
	}
	ret := make(map[string]interface{})
	if err := json.Unmarshal(res, &ret); err != nil {
		t.Fatalf("failed to unmarshal trace result: %v", err)
	}
	if _, has := ret["0x60f3f640a8508fc6a86d45df051962668e1e8ac7"]; !has {
		t.Fatalf("Expected 0x60f3f640a8508fc6a86d45df051962668e1e8ac7 in result")
	}
}

// Iterates over all the input-output datasets in the tracer test harness and
// runs the JavaScript tracers against them.
func TestCallTracer(t *testing.T) {
	files, filesErr := os.ReadDir("testdata")
	if filesErr != nil {
		t.Fatalf("failed to retrieve tracer test suite: %v", filesErr)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "call_tracer_") {
			continue
		}
		file := file // capture range variable
		t.Run(camel(strings.TrimSuffix(strings.TrimPrefix(file.Name(), "call_tracer_"), ".json")), func(t *testing.T) {
			t.Parallel()

			// Call tracer test found, read if from disk
			blob, blobErr := os.ReadFile(filepath.Join("testdata", file.Name()))
			if blobErr != nil {
				t.Fatalf("failed to read testcase: %v", blobErr)
			}
			test := new(callTracerTest)
			if err := json.Unmarshal(blob, test); err != nil {
				t.Fatalf("failed to parse testcase: %v", err)
			}
			// Configure a blockchain with the given prestate
			txn, err := types.DecodeTransaction(rlp.NewStream(bytes.NewReader(common.FromHex(test.Input)), 0))
			if err != nil {
				t.Fatalf("failed to parse testcase input: %v", err)
			}
			signer := types.MakeSigner(test.Genesis.Config, uint64(test.Context.Number))
			origin, _ := signer.Sender(txn)
			txContext := evmtypes.TxContext{
				Origin:   origin,
				GasPrice: txn.GetPrice(),
			}
			context := evmtypes.BlockContext{
				CanTransfer: core.CanTransfer,
				Transfer:    core.Transfer,
				Coinbase:    test.Context.Miner,
				BlockNumber: uint64(test.Context.Number),
				Time:        uint64(test.Context.Time),
				Difficulty:  (*big.Int)(test.Context.Difficulty),
				GasLimit:    uint64(test.Context.GasLimit),
			}

			_, tx := memdb.NewTestTx(t)
			rules := &params.Rules{}
			statedb, err := tests.MakePreState(rules, tx, test.Genesis.Alloc, uint64(test.Context.Number))
			require.NoError(t, err)

			// Create the tracer, the EVM environment and run it
			tracer, err := New("callTracer", new(Context))
			if err != nil {
				t.Fatalf("failed to create call tracer: %v", err)
			}
			evm := vm.NewEVM(context, txContext, statedb, test.Genesis.Config, vm.Config{Debug: true, Tracer: tracer})

			msg, err := txn.AsMessage(*signer, nil, rules)
			if err != nil {
				t.Fatalf("failed to prepare transaction for tracing: %v", err)
			}
			st := core.NewStateTransition(evm, msg, new(core.GasPool).AddGas(txn.GetGas()))
			if _, err = st.TransitionDb(false, false); err != nil {
				t.Fatalf("failed to execute transaction: %v", err)
			}
			// Retrieve the trace result and compare against the etalon
			res, err := tracer.GetResult()
			if err != nil {
				t.Fatalf("failed to retrieve trace result: %v", err)
			}
			ret := new(callTrace)
			if err := json.Unmarshal(res, ret); err != nil {
				t.Fatalf("failed to unmarshal trace result: %v", err)
			}

			if !jsonEqual(ret, test.Result) {
				// uncomment this for easier debugging
				//have, _ := json.MarshalIndent(ret, "", " ")
				//want, _ := json.MarshalIndent(test.Result, "", " ")
				//t.Fatalf("trace mismatch: \nhave %+v\nwant %+v", string(have), string(want))
				t.Fatalf("trace mismatch: \nhave %+v\nwant %+v", ret, test.Result)
			}
		})
	}
}

// jsonEqual is similar to reflect.DeepEqual, but does a 'bounce' via json prior to
// comparison
func jsonEqual(x, y interface{}) bool {
	xTrace := new(callTrace)
	yTrace := new(callTrace)
	if xj, err := json.Marshal(x); err == nil {
		if err = json.Unmarshal(xj, xTrace); err != nil {
			panic(err)
		}
	} else {
		return false
	}
	if yj, err := json.Marshal(y); err == nil {
		if err = json.Unmarshal(yj, yTrace); err != nil {
			panic(err)
		}
	} else {
		return false
	}
	return reflect.DeepEqual(xTrace, yTrace)
}