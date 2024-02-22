package itrie

import (
	"bytes"
	"github.com/0xPolygon/polygon-edge/crypto"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/umbracle/fastrlp"
)

type Snapshot struct {
	state *State
	trie  *Trie
}

var emptyStateHash = types.StringToHash("0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")

func (s *Snapshot) GetStorage(addr types.Address, root types.Hash, rawkey types.Hash) types.Hash {
	var (
		err  error
		trie *Trie
	)

	if root == emptyStateHash {
		trie = s.state.newTrie()
	} else {
		trie, err = s.state.newTrieAt(root)
		if err != nil {
			return types.Hash{}
		}
	}

	key := crypto.Keccak256(rawkey.Bytes())

	val, ok := trie.Get(key, s.state.storage)
	if !ok {
		return types.Hash{}
	}

	p := &fastrlp.Parser{}

	v, err := p.Parse(val)
	if err != nil {
		return types.Hash{}
	}

	res := []byte{}
	if res, err = v.GetBytes(res[:0]); err != nil {
		return types.Hash{}
	}

	return types.BytesToHash(res)
}

func (s *Snapshot) GetAccount(addr types.Address) (*state.Account, error) {
	key := crypto.Keccak256(addr.Bytes())

	data, ok := s.trie.Get(key, s.state.storage)
	if !ok {
		return nil, nil
	}

	var account state.Account
	if err := account.UnmarshalRlp(data); err != nil {
		return nil, err
	}

	return &account, nil
}

func (s *Snapshot) GetCode(hash types.Hash) ([]byte, bool) {
	return s.state.GetCode(hash)
}

func (s *Snapshot) Commit(objs []*state.Object) (state.Snapshot, []byte) {
	batch := s.state.storage.Batch()
	tt := s.trie.Txn(s.state.storage)
	tt.batch = batch

	accountArena := accountArenaPool.Get()
	defer accountArenaPool.Put(accountArena)

	stateArena := stateArenaPool.Get()
	defer stateArenaPool.Put(stateArena)

	for _, obj := range objs {
		addressHash := hashit(obj.Address.Bytes())

		if obj.Deleted {
			tt.Delete(addressHash)
			continue
		}

		account := state.Account{
			Balance:  obj.Balance,
			Nonce:    obj.Nonce,
			CodeHash: obj.CodeHash.Bytes(),
			Root:     obj.Root, // old root
		}

		// Update account storage
		if len(obj.Storage) != 0 {
			trie, err := s.state.newTrieAt(obj.Root)
			if err != nil {
				panic(err)
			}

			localTxn := trie.Txn(s.state.storage)
			localTxn.batch = batch

			for _, entry := range obj.Storage {
				keyHash := hashit(entry.Key)
				if entry.Deleted {
					localTxn.Delete(keyHash)
				} else {
					valueBytes := bytes.TrimLeft(entry.Val, "\x00")
					vv := stateArena.NewBytes(valueBytes)
					localTxn.Insert(keyHash, vv.MarshalTo(nil))
				}
			}

			accountStateRoot, _ := localTxn.Hash()
			accountStateTrie := localTxn.Commit()

			// Add to cache
			s.state.AddState(types.BytesToHash(accountStateRoot), accountStateTrie)

			account.Root = types.BytesToHash(accountStateRoot)
		}

		// Set code if dirty
		if obj.DirtyCode {
			s.state.SetCode(obj.CodeHash, obj.Code)
		}

		// Marshal account data
		data := account.MarshalWith(accountArena).MarshalTo(nil)

		// Insert into trie
		tt.Insert(addressHash, data)
	}

	root, _ := tt.Hash()
	nTrie := tt.Commit()

	// Write all the entries to db
	batch.Write()

	s.state.AddState(types.BytesToHash(root), nTrie)

	return &Snapshot{trie: nTrie, state: s.state}, root
}
