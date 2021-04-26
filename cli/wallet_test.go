package main

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/chzyer/readline"
	"github.com/nspcc-dev/neo-go/pkg/crypto/hash"
	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/encoding/address"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	"github.com/stretchr/testify/require"
)

func TestWalletInit(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "neogo.test.walletinit")
	require.NoError(t, os.Mkdir(tmpDir, os.ModePerm))
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	e := newExecutor(t, false)

	walletPath := path.Join(tmpDir, "wallet.json")
	e.Run(t, "neo-go", "wallet", "init", "--wallet", walletPath)

	t.Run("terminal escape codes", func(t *testing.T) {
		walletPath := path.Join(tmpDir, "walletrussian.json")
		bksp := string([]byte{
			byte(readline.CharBackward),
			byte(readline.CharDelete),
		})
		e.In.WriteString("буквыы" +
			bksp + bksp + bksp +
			"andmore\r")
		e.In.WriteString("пароу" + bksp + "ль\r")
		e.In.WriteString("пароль\r")
		e.Run(t, "neo-go", "wallet", "init", "--account",
			"--wallet", walletPath)

		w, err := wallet.NewWalletFromFile(walletPath)
		require.NoError(t, err)
		require.Len(t, w.Accounts, 1)
		require.Equal(t, "букandmore", w.Accounts[0].Label)
		require.NoError(t, w.Accounts[0].Decrypt("пароль"))
		w.Close()
	})

	t.Run("CreateAccount", func(t *testing.T) {
		e.In.WriteString("testname\r")
		e.In.WriteString("testpass\r")
		e.In.WriteString("testpass\r")
		e.Run(t, "neo-go", "wallet", "create", "--wallet", walletPath)

		w, err := wallet.NewWalletFromFile(walletPath)
		require.NoError(t, err)
		require.Len(t, w.Accounts, 1)
		require.Equal(t, w.Accounts[0].Label, "testname")
		require.NoError(t, w.Accounts[0].Decrypt("testpass"))
		w.Close()

		t.Run("RemoveAccount", func(t *testing.T) {
			sh := w.Accounts[0].Contract.ScriptHash()
			addr := w.Accounts[0].Address
			e.In.WriteString("y\r")
			e.Run(t, "neo-go", "wallet", "remove",
				"--wallet", walletPath, "--address", addr)
			w, err := wallet.NewWalletFromFile(walletPath)
			require.NoError(t, err)
			require.Nil(t, w.GetAccount(sh))
			w.Close()
		})
	})

	t.Run("Import", func(t *testing.T) {
		t.Run("WIF", func(t *testing.T) {
			priv, err := keys.NewPrivateKey()
			require.NoError(t, err)
			e.In.WriteString("test_account\r")
			e.In.WriteString("qwerty\r")
			e.In.WriteString("qwerty\r")
			e.Run(t, "neo-go", "wallet", "import", "--wallet", walletPath,
				"--wif", priv.WIF())

			w, err := wallet.NewWalletFromFile(walletPath)
			require.NoError(t, err)
			t.Cleanup(w.Close)
			acc := w.GetAccount(priv.GetScriptHash())
			require.NotNil(t, acc)
			require.Equal(t, "test_account", acc.Label)
			require.NoError(t, acc.Decrypt("qwerty"))

			t.Run("AlreadyExists", func(t *testing.T) {
				e.In.WriteString("test_account_2\r")
				e.In.WriteString("qwerty2\r")
				e.In.WriteString("qwerty2\r")
				e.RunWithError(t, "neo-go", "wallet", "import",
					"--wallet", walletPath, "--wif", priv.WIF())
			})
		})
		t.Run("EncryptedWIF", func(t *testing.T) {
			acc, err := wallet.NewAccount()
			require.NoError(t, err)
			require.NoError(t, acc.Encrypt("somepass"))

			t.Run("InvalidPassword", func(t *testing.T) {
				e.In.WriteString("password1\r")
				e.RunWithError(t, "neo-go", "wallet", "import", "--wallet", walletPath,
					"--wif", acc.EncryptedWIF)
			})

			e.In.WriteString("somepass\r")
			e.Run(t, "neo-go", "wallet", "import", "--wallet", walletPath,
				"--wif", acc.EncryptedWIF)

			w, err := wallet.NewWalletFromFile(walletPath)
			require.NoError(t, err)
			t.Cleanup(w.Close)
			actual := w.GetAccount(acc.PrivateKey().GetScriptHash())
			require.NotNil(t, actual)
			require.NoError(t, actual.Decrypt("somepass"))
		})
		t.Run("Multisig", func(t *testing.T) {
			privs, pubs := generateKeys(t, 4)

			cmd := []string{"neo-go", "wallet", "import-multisig",
				"--wallet", walletPath,
				"--wif", privs[0].WIF(),
				"--min", "2"}
			t.Run("InvalidPublicKeys", func(t *testing.T) {
				e.In.WriteString("multiacc\r")
				e.In.WriteString("multipass\r")
				e.In.WriteString("multipass\r")
				defer e.In.Reset()

				e.RunWithError(t, append(cmd, hex.EncodeToString(pubs[1].Bytes()),
					hex.EncodeToString(pubs[1].Bytes()),
					hex.EncodeToString(pubs[2].Bytes()),
					hex.EncodeToString(pubs[3].Bytes()))...)
			})
			e.In.WriteString("multiacc\r")
			e.In.WriteString("multipass\r")
			e.In.WriteString("multipass\r")
			e.Run(t, append(cmd, hex.EncodeToString(pubs[0].Bytes()),
				hex.EncodeToString(pubs[1].Bytes()),
				hex.EncodeToString(pubs[2].Bytes()),
				hex.EncodeToString(pubs[3].Bytes()))...)

			script, err := smartcontract.CreateMultiSigRedeemScript(2, pubs)
			require.NoError(t, err)

			w, err := wallet.NewWalletFromFile(walletPath)
			require.NoError(t, err)
			t.Cleanup(w.Close)
			actual := w.GetAccount(hash.Hash160(script))
			require.NotNil(t, actual)
			require.NoError(t, actual.Decrypt("multipass"))
			require.Equal(t, script, actual.Contract.Script)
		})
	})
}

func TestWalletExport(t *testing.T) {
	e := newExecutor(t, false)

	t.Run("Encrypted", func(t *testing.T) {
		e.Run(t, "neo-go", "wallet", "export",
			"--wallet", validatorWallet, validatorAddr)
		line, err := e.Out.ReadString('\n')
		require.NoError(t, err)
		enc, err := keys.NEP2Encrypt(validatorPriv, "one")
		require.NoError(t, err)
		require.Equal(t, enc, strings.TrimSpace(line))
	})
	t.Run("Decrypted", func(t *testing.T) {
		t.Run("NoAddress", func(t *testing.T) {
			e.RunWithError(t, "neo-go", "wallet", "export",
				"--wallet", validatorWallet, "--decrypt")
		})
		e.In.WriteString("one\r")
		e.Run(t, "neo-go", "wallet", "export",
			"--wallet", validatorWallet, "--decrypt", validatorAddr)
		line, err := e.Out.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, validatorWIF, strings.TrimSpace(line))
	})
}

func TestClaimGas(t *testing.T) {
	e := newExecutor(t, true)

	const walletPath = "testdata/testwallet.json"
	w, err := wallet.NewWalletFromFile(walletPath)
	require.NoError(t, err)
	t.Cleanup(w.Close)

	args := []string{
		"neo-go", "wallet", "nep17", "multitransfer",
		"--rpc-endpoint", "http://" + e.RPC.Addr,
		"--wallet", validatorWallet,
		"--from", validatorAddr,
		"NEO:" + w.Accounts[0].Address + ":1000",
		"GAS:" + w.Accounts[0].Address + ":1000", // for tx send
	}
	e.In.WriteString("one\r")
	e.Run(t, args...)
	e.checkTxPersisted(t)

	h, err := address.StringToUint160(w.Accounts[0].Address)
	require.NoError(t, err)

	balanceBefore := e.Chain.GetUtilityTokenBalance(h)
	claimHeight := e.Chain.BlockHeight() + 1
	cl, err := e.Chain.CalculateClaimable(h, claimHeight)
	require.NoError(t, err)
	require.True(t, cl.Sign() > 0)

	e.In.WriteString("testpass\r")
	e.Run(t, "neo-go", "wallet", "claim",
		"--rpc-endpoint", "http://"+e.RPC.Addr,
		"--wallet", walletPath,
		"--address", w.Accounts[0].Address)
	tx, height := e.checkTxPersisted(t)
	balanceBefore.Sub(balanceBefore, big.NewInt(tx.NetworkFee+tx.SystemFee))
	balanceBefore.Add(balanceBefore, cl)

	balanceAfter := e.Chain.GetUtilityTokenBalance(h)
	// height can be bigger than claimHeight especially when tests are executed with -race.
	if height == claimHeight {
		require.Equal(t, 0, balanceAfter.Cmp(balanceBefore))
	} else {
		require.Equal(t, 1, balanceAfter.Cmp(balanceBefore))
	}
}

func TestImportDeployed(t *testing.T) {
	e := newExecutor(t, true)

	h := deployVerifyContract(t, e)
	tmpDir := os.TempDir()
	walletPath := path.Join(tmpDir, "wallet.json")
	e.Run(t, "neo-go", "wallet", "init", "--wallet", walletPath)
	t.Cleanup(func() {
		os.Remove(walletPath)
	})

	priv, err := keys.NewPrivateKey()
	require.NoError(t, err)

	// missing contract sh
	e.RunWithError(t, "neo-go", "wallet", "import-deployed",
		"--rpc-endpoint", "http://"+e.RPC.Addr,
		"--wallet", walletPath, "--wif", priv.WIF())

	e.In.WriteString("acc\rpass\rpass\r")
	e.Run(t, "neo-go", "wallet", "import-deployed",
		"--rpc-endpoint", "http://"+e.RPC.Addr,
		"--wallet", walletPath, "--wif", priv.WIF(),
		"--contract", h.StringLE())

	w, err := wallet.NewWalletFromFile(walletPath)
	t.Cleanup(func() {
		w.Close()
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(w.Accounts))
	contractAddr := w.Accounts[0].Address
	require.Equal(t, address.Uint160ToString(h), contractAddr)
	require.True(t, w.Accounts[0].Contract.Deployed)

	t.Run("Sign", func(t *testing.T) {
		e.In.WriteString("one\r")
		e.Run(t, "neo-go", "wallet", "nep17", "multitransfer",
			"--rpc-endpoint", "http://"+e.RPC.Addr,
			"--wallet", validatorWallet, "--from", validatorAddr,
			"NEO:"+contractAddr+":10",
			"GAS:"+contractAddr+":10")
		e.checkTxPersisted(t)

		privTo, err := keys.NewPrivateKey()
		require.NoError(t, err)

		e.In.WriteString("pass\r")
		e.Run(t, "neo-go", "wallet", "nep17", "transfer",
			"--rpc-endpoint", "http://"+e.RPC.Addr,
			"--wallet", walletPath, "--from", contractAddr,
			"--to", privTo.Address(), "--token", "NEO", "--amount", "1")
		e.checkTxPersisted(t)

		b, _ := e.Chain.GetGoverningTokenBalance(h)
		require.Equal(t, big.NewInt(9), b)
		b, _ = e.Chain.GetGoverningTokenBalance(privTo.GetScriptHash())
		require.Equal(t, big.NewInt(1), b)
	})
}

func TestWalletDump(t *testing.T) {
	e := newExecutor(t, false)

	cmd := []string{"neo-go", "wallet", "dump", "--wallet", "testdata/testwallet.json"}
	e.Run(t, cmd...)
	rawStr := strings.TrimSpace(e.Out.String())
	w := new(wallet.Wallet)
	require.NoError(t, json.Unmarshal([]byte(rawStr), w))
	require.Equal(t, 1, len(w.Accounts))
	require.Equal(t, "NUSEsqon6PikQA5mDFaV4njemF9Su8JEmf", w.Accounts[0].Address)

	t.Run("with decrypt", func(t *testing.T) {
		cmd = append(cmd, "--decrypt")
		t.Run("invalid password", func(t *testing.T) {
			e.In.WriteString("invalidpass\r")
			e.RunWithError(t, cmd...)
		})

		e.In.WriteString("testpass\r")
		e.Run(t, cmd...)
		rawStr := strings.TrimSpace(e.Out.String())
		w := new(wallet.Wallet)
		require.NoError(t, json.Unmarshal([]byte(rawStr), w))
		require.Equal(t, 1, len(w.Accounts))
		require.Equal(t, "NUSEsqon6PikQA5mDFaV4njemF9Su8JEmf", w.Accounts[0].Address)
	})
}

func TestDumpKeys(t *testing.T) {
	e := newExecutor(t, false)
	cmd := []string{"neo-go", "wallet", "dump-keys", "--wallet", validatorWallet}
	pubRegex := "^0[23][a-hA-H0-9]{64}$"
	t.Run("all", func(t *testing.T) {
		e.Run(t, cmd...)
		e.checkNextLine(t, "NTh9TnZTstvAePEYWDGLLxidBikJE24uTo")
		e.checkNextLine(t, pubRegex)
		e.checkNextLine(t, "^\\s*$")
		e.checkNextLine(t, "NgEisvCqr2h8wpRxQb7bVPWUZdbVCY8Uo6")
		for i := 0; i < 4; i++ {
			e.checkNextLine(t, pubRegex)
		}
		e.checkNextLine(t, "^\\s*$")
		e.checkNextLine(t, "NNudMSGzEoktFzdYGYoNb3bzHzbmM1genF")
		e.checkNextLine(t, pubRegex)
		e.checkEOF(t)
	})
	t.Run("simple signature", func(t *testing.T) {
		cmd := append(cmd, "--address", "NTh9TnZTstvAePEYWDGLLxidBikJE24uTo")
		e.Run(t, cmd...)
		e.checkNextLine(t, "simple signature contract")
		e.checkNextLine(t, pubRegex)
		e.checkEOF(t)
	})
	t.Run("3/4 multisig", func(t *testing.T) {
		cmd := append(cmd, "-a", "NgEisvCqr2h8wpRxQb7bVPWUZdbVCY8Uo6")
		e.Run(t, cmd...)
		e.checkNextLine(t, "3 out of 4 multisig contract")
		for i := 0; i < 4; i++ {
			e.checkNextLine(t, pubRegex)
		}
		e.checkEOF(t)
	})
	t.Run("1/1 multisig", func(t *testing.T) {
		cmd := append(cmd, "--address", "NNudMSGzEoktFzdYGYoNb3bzHzbmM1genF")
		e.Run(t, cmd...)
		e.checkNextLine(t, "1 out of 1 multisig contract")
		e.checkNextLine(t, pubRegex)
		e.checkEOF(t)
	})
}

// Testcase is the wallet of privnet validator.
func TestWalletConvert(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "neogo.test.convert")
	require.NoError(t, os.Mkdir(tmpDir, os.ModePerm))
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	e := newExecutor(t, false)

	outPath := path.Join(tmpDir, "wallet.json")
	cmd := []string{"neo-go", "wallet", "convert"}
	t.Run("missing wallet", func(t *testing.T) {
		e.RunWithError(t, cmd...)
	})

	cmd = append(cmd, "--wallet", "testdata/wallets/testwallet_NEO2.json", "--out", outPath)
	t.Run("invalid password", func(t *testing.T) {
		// missing password
		e.RunWithError(t, cmd...)
		// invalid password
		e.In.WriteString("two\r")
		e.RunWithError(t, cmd...)
	})

	// 2 accounts.
	e.In.WriteString("one\r")
	e.In.WriteString("one\r")
	e.Run(t, "neo-go", "wallet", "convert",
		"--wallet", "testdata/wallets/testwallet_NEO2.json",
		"--out", outPath)

	actual, err := wallet.NewWalletFromFile(outPath)
	require.NoError(t, err)
	expected, err := wallet.NewWalletFromFile("testdata/wallets/testwallet_NEO3.json")
	require.NoError(t, err)
	require.Equal(t, len(actual.Accounts), len(expected.Accounts))
	for _, exp := range expected.Accounts {
		addr, err := address.StringToUint160(exp.Address)
		require.NoError(t, err)

		act := actual.GetAccount(addr)
		require.NotNil(t, act)
		require.Equal(t, exp, act)
	}
}
