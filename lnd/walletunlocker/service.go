package walletunlocker

import (
	"context"
	"io/ioutil"
	"os"
	"strings"

	"github.com/pkt-cash/pktd/btcutil/er"
	"github.com/pkt-cash/pktd/chaincfg"
	"github.com/pkt-cash/pktd/lnd/chanbackup"
	"github.com/pkt-cash/pktd/lnd/lncfg"
	"github.com/pkt-cash/pktd/lnd/lnrpc"
	"github.com/pkt-cash/pktd/lnd/lnwallet"
	"github.com/pkt-cash/pktd/lnd/lnwallet/btcwallet"
	"github.com/pkt-cash/pktd/lnd/macaroons"
	"github.com/pkt-cash/pktd/pktlog/log"
	"github.com/pkt-cash/pktd/pktwallet/wallet"
	"github.com/pkt-cash/pktd/pktwallet/wallet/seedwords"
)

var (
	// ErrUnlockTimeout signals that we did not get the expected unlock
	// message before the timeout occurred.
	ErrUnlockTimeout = er.GenericErrorType.CodeWithDetail("ErrUnlockTimeout",
		"got no unlock message before timeout")
)

// ChannelsToRecover wraps any set of packed (serialized+encrypted) channel
// back ups together. These can be passed in when unlocking the wallet, or
// creating a new wallet for the first time with an existing seed.
type ChannelsToRecover struct {
	// PackedMultiChanBackup is an encrypted and serialized multi-channel
	// backup.
	PackedMultiChanBackup chanbackup.PackedMulti

	// PackedSingleChanBackups is a series of encrypted and serialized
	// single-channel backup for one or more channels.
	PackedSingleChanBackups chanbackup.PackedSingles
}

// WalletInitMsg is a message sent by the UnlockerService when a user wishes to
// set up the internal wallet for the first time. The user MUST provide a
// passphrase, but is also able to provide their own source of entropy. If
// provided, then this source of entropy will be used to generate the wallet's
// HD seed. Otherwise, the wallet will generate one itself.
type WalletInitMsg struct {
	// Passphrase is the passphrase that will be used to encrypt the wallet
	// itself. This MUST be at least 8 characters.
	Passphrase []byte

	// WalletSeed is the deciphered cipher seed that the wallet should use
	// to initialize itself.
	Seed *seedwords.Seed

	// RecoveryWindow is the address look-ahead used when restoring a seed
	// with existing funds. A recovery window zero indicates that no
	// recovery should be attempted, such as after the wallet's initial
	// creation.
	RecoveryWindow uint32

	// ChanBackups a set of static channel backups that should be received
	// after the wallet has been initialized.
	ChanBackups ChannelsToRecover

	// StatelessInit signals that the user requested the daemon to be
	// initialized stateless, which means no unencrypted macaroons should be
	// written to disk.
	StatelessInit bool
}

// WalletUnlockMsg is a message sent by the UnlockerService when a user wishes
// to unlock the internal wallet after initial setup. The user can optionally
// specify a recovery window, which will resume an interrupted rescan for used
// addresses.
type WalletUnlockMsg struct {
	// Passphrase is the passphrase that will be used to encrypt the wallet
	// itself. This MUST be at least 8 characters.
	Passphrase []byte

	// RecoveryWindow is the address look-ahead used when restoring a seed
	// with existing funds. A recovery window zero indicates that no
	// recovery should be attempted, such as after the wallet's initial
	// creation, but before any addresses have been created.
	RecoveryWindow uint32

	// Wallet is the loaded and unlocked Wallet. This is returned through
	// the channel to avoid it being unlocked twice (once to check if the
	// password is correct, here in the WalletUnlocker and again later when
	// lnd actually uses it). Because unlocking involves scrypt which is
	// resource intensive, we want to avoid doing it twice.
	Wallet *wallet.Wallet

	// ChanBackups a set of static channel backups that should be received
	// after the wallet has been unlocked.
	ChanBackups ChannelsToRecover

	// UnloadWallet is a function for unloading the wallet, which should
	// be called on shutdown.
	UnloadWallet func() er.R

	// StatelessInit signals that the user requested the daemon to be
	// initialized stateless, which means no unencrypted macaroons should be
	// written to disk.
	StatelessInit bool
}

// UnlockerService implements the WalletUnlocker service used to provide lnd
// with a password for wallet encryption at startup. Additionally, during
// initial setup, users can provide their own source of entropy which will be
// used to generate the seed that's ultimately used within the wallet.
type UnlockerService struct {
	// InitMsgs is a channel that carries all wallet init messages.
	InitMsgs chan *WalletInitMsg

	// UnlockMsgs is a channel where unlock parameters provided by the rpc
	// client to be used to unlock and decrypt an existing wallet will be
	// sent.
	UnlockMsgs chan *WalletUnlockMsg

	// MacResponseChan is the channel for sending back the admin macaroon to
	// the WalletUnlocker service.
	MacResponseChan chan []byte

	chainDir       string
	noFreelistSync bool
	netParams      *chaincfg.Params

	// macaroonFiles is the path to the three generated macaroons with
	// different access permissions. These might not exist in a stateless
	// initialization of lnd.
	macaroonFiles []string

	walletFile string
	walletPath string
}

var _ lnrpc.WalletUnlockerServer = (*UnlockerService)(nil)

// New creates and returns a new UnlockerService.
func New(chainDir string, params *chaincfg.Params, noFreelistSync bool,
	macaroonFiles []string, walletPath string, walletFilename string) *UnlockerService {

	return &UnlockerService{
		InitMsgs:   make(chan *WalletInitMsg, 1),
		UnlockMsgs: make(chan *WalletUnlockMsg, 1),

		// Make sure we buffer the channel is buffered so the main lnd
		// goroutine isn't blocking on writing to it.
		MacResponseChan: make(chan []byte, 1),
		chainDir:        chainDir,
		netParams:       params,
		macaroonFiles:   macaroonFiles,
		walletFile:      walletFilename,
		walletPath:      walletPath,
	}
}

func (u *UnlockerService) GenSeed(_ context.Context,
	in *lnrpc.GenSeedRequest) (*lnrpc.GenSeedResponse, error) {
	res, err := u.GenSeed0(nil, in)
	return res, er.Native(err)
}

// GenSeed is the first method that should be used to instantiate a new lnd
// instance. This method allows a caller to generate a new aezeed cipher seed
// given an optional passphrase. If provided, the passphrase will be necessary
// to decrypt the cipherseed to expose the internal wallet seed.
//
// Once the cipherseed is obtained and verified by the user, the InitWallet
// method should be used to commit the newly generated seed, and create the
// wallet.
func (u *UnlockerService) GenSeed0(_ context.Context,
	in *lnrpc.GenSeedRequest) (*lnrpc.GenSeedResponse, er.R) {

	// Before we start, we'll ensure that the wallet hasn't already created
	// so we don't show a *new* seed to the user if one already exists.
	netDir := btcwallet.NetworkDir(u.chainDir, u.netParams)
	if u.walletPath != "" {
		netDir = u.walletPath
	}
	loader := wallet.NewLoader(u.netParams, netDir, u.walletFile, u.noFreelistSync, 0)
	walletExists, err := loader.WalletExists()
	if err != nil {
		return nil, err
	}
	if walletExists {
		return nil, er.Errorf("wallet already exists")
	}

	//var entropy [aezeed.EntropySize]byte

	if len(in.SeedEntropy) != 0 {
		return nil, er.Errorf("seed input entropy is not supported")
	}

	// Now that we have our set of entropy, we'll create a new cipher seed
	// instance.
	//
	cipherSeed, err := seedwords.RandomSeed()
	if err != nil {
		return nil, err
	}

	encipheredSeed := cipherSeed.Encrypt(in.AezeedPassphrase)

	mnemonic, err := encipheredSeed.Words("english")
	if err != nil {
		return nil, err
	}

	return &lnrpc.GenSeedResponse{
		CipherSeedMnemonic: strings.Split(mnemonic, " "),
		EncipheredSeed:     encipheredSeed.Bytes[:],
	}, nil
}

// extractChanBackups is a helper function that extracts the set of channel
// backups from the proto into a format that we'll pass to higher level
// sub-systems.
func extractChanBackups(chanBackups *lnrpc.ChanBackupSnapshot) *ChannelsToRecover {
	// If there aren't any populated channel backups, then we can exit
	// early as there's nothing to extract.
	if chanBackups == nil || (chanBackups.SingleChanBackups == nil &&
		chanBackups.MultiChanBackup == nil) {
		return nil
	}

	// Now that we know there's at least a single back up populated, we'll
	// extract the multi-chan backup (if it's there).
	var backups ChannelsToRecover
	if chanBackups.MultiChanBackup != nil {
		multiBackup := chanBackups.MultiChanBackup
		backups.PackedMultiChanBackup = chanbackup.PackedMulti(
			multiBackup.MultiChanBackup,
		)
	}

	if chanBackups.SingleChanBackups == nil {
		return &backups
	}

	// Finally, we can extract all the single chan backups as well.
	for _, backup := range chanBackups.SingleChanBackups.ChanBackups {
		singleChanBackup := backup.ChanBackup

		backups.PackedSingleChanBackups = append(
			backups.PackedSingleChanBackups, singleChanBackup,
		)
	}

	return &backups
}

func (u *UnlockerService) InitWallet(ctx context.Context,
	in *lnrpc.InitWalletRequest) (*lnrpc.InitWalletResponse, error) {
	res, err := u.InitWallet0(ctx, in)
	return res, er.Native(err)
}

// InitWallet is used when lnd is starting up for the first time to fully
// initialize the daemon and its internal wallet. At the very least a wallet
// password must be provided. This will be used to encrypt sensitive material
// on disk.
//
// In the case of a recovery scenario, the user can also specify their aezeed
// mnemonic and passphrase. If set, then the daemon will use this prior state
// to initialize its internal wallet.
//
// Alternatively, this can be used along with the GenSeed RPC to obtain a
// seed, then present it to the user. Once it has been verified by the user,
// the seed can be fed into this RPC in order to commit the new wallet.
func (u *UnlockerService) InitWallet0(ctx context.Context,
	in *lnrpc.InitWalletRequest) (*lnrpc.InitWalletResponse, er.R) {

	// Make sure the password meets our constraints.
	password := in.WalletPassword
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}

	// Require that the recovery window be non-negative.
	recoveryWindow := in.RecoveryWindow
	if recoveryWindow < 0 {
		return nil, er.Errorf("recovery window %d must be "+
			"non-negative", recoveryWindow)
	}

	// We'll then open up the directory that will be used to store the
	// wallet's files so we can check if the wallet already exists.
	netDir := btcwallet.NetworkDir(u.chainDir, u.netParams)
	if u.walletPath != "" {
		netDir = u.walletPath
	}
	loader := wallet.NewLoader(u.netParams, netDir, u.walletFile, u.noFreelistSync, uint32(recoveryWindow))

	walletExists, err := loader.WalletExists()
	if err != nil {
		return nil, err
	}

	// If the wallet already exists, then we'll exit early as we can't
	// create the wallet if it already exists!
	if walletExists {
		return nil, er.Errorf("wallet already exists")
	}

	mnemonic := strings.Join(in.CipherSeedMnemonic, " ")
	seedEnc, err := seedwords.SeedFromWords(mnemonic)
	if err != nil {
		return nil, err
	}

	seed, err := seedEnc.Decrypt(in.AezeedPassphrase, false)
	if err != nil {
		return nil, err
	}

	// With the cipher seed deciphered, and the auth service created, we'll
	// now send over the wallet password and the seed. This will allow the
	// daemon to initialize itself and startup.
	initMsg := &WalletInitMsg{
		Passphrase:     password,
		Seed:           seed,
		RecoveryWindow: uint32(recoveryWindow),
		StatelessInit:  in.StatelessInit,
	}

	// Before we return the unlock payload, we'll check if we can extract
	// any channel backups to pass up to the higher level sub-system.
	chansToRestore := extractChanBackups(in.ChannelBackups)
	if chansToRestore != nil {
		initMsg.ChanBackups = *chansToRestore
	}

	// Deliver the initialization message back to the main daemon.
	select {
	case u.InitMsgs <- initMsg:
		// We need to read from the channel to let the daemon continue
		// its work and to get the admin macaroon. Once the response
		// arrives, we directly forward it to the client.
		select {
		case adminMac := <-u.MacResponseChan:
			return &lnrpc.InitWalletResponse{
				AdminMacaroon: adminMac,
			}, nil

		case <-ctx.Done():
			return nil, ErrUnlockTimeout.Default()
		}

	case <-ctx.Done():
		return nil, ErrUnlockTimeout.Default()
	}
}

func (u *UnlockerService) UnlockWallet(ctx context.Context,
	in *lnrpc.UnlockWalletRequest) (*lnrpc.UnlockWalletResponse, error) {
	res, err := u.UnlockWallet0(ctx, in)
	return res, er.Native(err)
}

// UnlockWallet sends the password provided by the incoming UnlockWalletRequest
// over the UnlockMsgs channel in case it successfully decrypts an existing
// wallet found in the chain's wallet database directory.
func (u *UnlockerService) UnlockWallet0(ctx context.Context,
	in *lnrpc.UnlockWalletRequest) (*lnrpc.UnlockWalletResponse, er.R) {

	privpassword := in.WalletPassword
	pubpassword := []byte(wallet.InsecurePubPassphrase)
	if in.WalletPubPassword != nil {
		pubpassword = in.WalletPubPassword
	}

	recoveryWindow := uint32(in.RecoveryWindow)

	netDir := btcwallet.NetworkDir(u.chainDir, u.netParams)
	if u.walletPath != "" {
		netDir = u.walletPath
	}
	loader := wallet.NewLoader(u.netParams, netDir, u.walletFile, u.noFreelistSync, recoveryWindow)

	// Check if wallet already exists.
	walletExists, err := loader.WalletExists()
	if err != nil {
		return nil, err
	}

	if !walletExists {
		// Cannot unlock a wallet that does not exist!
		return nil, er.Errorf("wallet not found at path [%s/wallet.db]", netDir)
	}

	// Try opening the existing wallet with the provided password.
	unlockedWallet, err := loader.OpenExistingWallet(pubpassword, false)
	if err != nil {
		// Could not open wallet, most likely this means that provided
		// password was incorrect.
		return nil, err
	}

	// We successfully opened the wallet and pass the instance back to
	// avoid it needing to be unlocked again.
	walletUnlockMsg := &WalletUnlockMsg{
		Passphrase:     privpassword,
		RecoveryWindow: recoveryWindow,
		Wallet:         unlockedWallet,
		UnloadWallet:   loader.UnloadWallet,
		StatelessInit:  in.StatelessInit,
	}

	// Before we return the unlock payload, we'll check if we can extract
	// any channel backups to pass up to the higher level sub-system.
	chansToRestore := extractChanBackups(in.ChannelBackups)
	if chansToRestore != nil {
		walletUnlockMsg.ChanBackups = *chansToRestore
	}

	// At this point we were able to open the existing wallet with the
	// provided password. We send the password over the UnlockMsgs
	// channel, such that it can be used by lnd to open the wallet.
	select {
	case u.UnlockMsgs <- walletUnlockMsg:
		// We need to read from the channel to let the daemon continue
		// its work. But we don't need the returned macaroon for this
		// operation, so we read it but then discard it.
		select {
		case <-u.MacResponseChan:
			return &lnrpc.UnlockWalletResponse{}, nil

		case <-ctx.Done():
			return nil, ErrUnlockTimeout.Default()
		}

	case <-ctx.Done():
		return nil, ErrUnlockTimeout.Default()
	}
}

func (u *UnlockerService) ChangePassword(ctx context.Context,
	in *lnrpc.ChangePasswordRequest) (*lnrpc.ChangePasswordResponse, error) {
	res, err := u.ChangePassword0(ctx, in)
	return res, er.Native(err)
}

// ChangePassword changes the password of the wallet and sends the new password
// across the UnlockPasswords channel to automatically unlock the wallet if
// successful.
func (u *UnlockerService) ChangePassword0(ctx context.Context,
	in *lnrpc.ChangePasswordRequest) (*lnrpc.ChangePasswordResponse, er.R) {

	netDir := btcwallet.NetworkDir(u.chainDir, u.netParams)
	if u.walletPath != "" {
		netDir = u.walletPath
	}
	loader := wallet.NewLoader(u.netParams, netDir, u.walletFile, u.noFreelistSync, 0)

	// First, we'll make sure the wallet exists for the specific chain and
	// network.
	walletExists, err := loader.WalletExists()
	if err != nil {
		return nil, err
	}

	if !walletExists {
		return nil, er.New("wallet not found")
	}

	publicPw := in.CurrentPassword
	privatePw := in.CurrentPassword

	// If the current password is blank, we'll assume the user is coming
	// from a --noseedbackup state, so we'll use the default passwords.
	if len(in.CurrentPassword) == 0 {
		publicPw = lnwallet.DefaultPublicPassphrase
		privatePw = lnwallet.DefaultPrivatePassphrase
	}

	// Make sure the new password meets our constraints.
	if err := ValidatePassword(in.NewPassword); err != nil {
		return nil, err
	}

	// Load the existing wallet in order to proceed with the password change.
	w, err := loader.OpenExistingWallet(publicPw, false)
	if err != nil {
		return nil, err
	}

	// Now that we've opened the wallet, we need to close it in case of an
	// error. But not if we succeed, then the caller must close it.
	orderlyReturn := false
	defer func() {
		if !orderlyReturn {
			_ = loader.UnloadWallet()
		}
	}()

	// Before we actually change the password, we need to check if all flags
	// were set correctly. The content of the previously generated macaroon
	// files will become invalid after we generate a new root key. So we try
	// to delete them here and they will be recreated during normal startup
	// later. If they are missing, this is only an error if the
	// stateless_init flag was not set.
	if in.NewMacaroonRootKey || in.StatelessInit {
		for _, file := range u.macaroonFiles {
			err := os.Remove(file)
			if err != nil && !in.StatelessInit {
				return nil, er.Errorf("could not remove "+
					"macaroon file: %v. if the wallet "+
					"was initialized stateless please "+
					"add the --stateless_init "+
					"flag", err)
			}
		}
	}

	// Attempt to change both the public and private passphrases for the
	// wallet. This will be done atomically in order to prevent one
	// passphrase change from being successful and not the other.
	err = w.ChangePassphrases(
		publicPw, in.NewPassword, privatePw, in.NewPassword,
	)
	if err != nil {
		return nil, er.Errorf("unable to change wallet passphrase: "+
			"%v", err)
	}

	// The next step is to load the macaroon database, change the password
	// then close it again.
	// Attempt to open the macaroon DB, unlock it and then change
	// the passphrase.
	macaroonService, err := macaroons.NewService(
		netDir, "lnd", in.StatelessInit,
	)
	if err != nil {
		return nil, err
	}

	err = macaroonService.CreateUnlock(&privatePw)
	if err != nil {
		closeErr := macaroonService.Close()
		if closeErr != nil {
			return nil, er.Errorf("could not create unlock: %v "+
				"--> follow-up error when closing: %v", err,
				closeErr)
		}
		return nil, err
	}
	err = macaroonService.ChangePassword(privatePw, in.NewPassword)
	if err != nil {
		closeErr := macaroonService.Close()
		if closeErr != nil {
			return nil, er.Errorf("could not change password: %v "+
				"--> follow-up error when closing: %v", err,
				closeErr)
		}
		return nil, err
	}

	// If requested by the user, attempt to replace the existing
	// macaroon root key with a new one.
	if in.NewMacaroonRootKey {
		err = macaroonService.GenerateNewRootKey()
		if err != nil {
			closeErr := macaroonService.Close()
			if closeErr != nil {
				return nil, er.Errorf("could not generate "+
					"new root key: %v --> follow-up error "+
					"when closing: %v", err, closeErr)
			}
			return nil, err
		}
	}

	err = macaroonService.Close()
	if err != nil {
		return nil, er.Errorf("could not close macaroon service: %v",
			err)
	}

	// Finally, send the new password across the UnlockPasswords channel to
	// automatically unlock the wallet.
	walletUnlockMsg := &WalletUnlockMsg{
		Passphrase:    in.NewPassword,
		Wallet:        w,
		StatelessInit: in.StatelessInit,
		UnloadWallet:  loader.UnloadWallet,
	}
	select {
	case u.UnlockMsgs <- walletUnlockMsg:
		// We need to read from the channel to let the daemon continue
		// its work and to get the admin macaroon. Once the response
		// arrives, we directly forward it to the client.
		orderlyReturn = true
		select {
		case adminMac := <-u.MacResponseChan:
			return &lnrpc.ChangePasswordResponse{
				AdminMacaroon: adminMac,
			}, nil

		case <-ctx.Done():
			return nil, ErrUnlockTimeout.Default()
		}

	case <-ctx.Done():
		return nil, ErrUnlockTimeout.Default()
	}
}

// ValidatePassword assures the password meets all of our constraints.
func ValidatePassword(password []byte) er.R {
	// Passwords should have a length of at least 8 characters.
	if len(password) < 8 {
		return er.New("password must have at least 8 characters")
	}

	return nil
}

func (u *UnlockerService) CreateWallet(ctx context.Context, req *lnrpc.CreateWalletRequest) (*lnrpc.CreateWalletResponse, error) {
	response := &lnrpc.CreateWalletResponse{}
	var cipherSeed []string
	var aezeedPass []byte
	//Validate password
	err := ValidatePassword(req.WalletPassword)
	if err != nil {
		log.Infof("Password could not be validated.")
		return response, er.Native(err)
	}
	if req.CipherSeedMnemonic != nil {
		cipherSeed = req.CipherSeedMnemonic
		log.Infof("Using provided cipher seed mnemonic.")
		//Check Seed Mnemonic
		if len(req.CipherSeedMnemonic) != 15 {
			return response, er.Native(er.New("wrong cipher seed mnemonic length: got " + string(len(req.CipherSeedMnemonic)) + " words, expecting 15 words"))
		}
		cipherSeedString := strings.Join(cipherSeed, " ")
		seedEnc, err := seedwords.SeedFromWords(cipherSeedString)
		if err != nil {
			return response, er.Native(err)
		}
		if seedEnc.NeedsPassphrase() && req.AezeedPass == nil {
			return response, er.Native(er.New("This seed is encrypted aezeedPassphrase provided is empty"))
		}
	} else {
		log.Infof("Generating seed.")
		if req.AezeedPass != nil {
			aezeedPass = req.AezeedPass
		}
		//passphrase for encrypting seed
		genSeedReq := &lnrpc.GenSeedRequest{
			AezeedPassphrase: aezeedPass,
		}
		seedResp, err := u.GenSeed(ctx, genSeedReq)
		if err != nil {
			return response, er.Native(er.New("unable to generate seed: " + err.Error()))
		}
		cipherSeed = seedResp.CipherSeedMnemonic
	}

	statelessInit := req.StatelessInitFlag
	var chanBackups *lnrpc.ChanBackupSnapshot
	initWalletRequest := &lnrpc.InitWalletRequest{
		WalletPassword:     req.WalletPassword,
		CipherSeedMnemonic: cipherSeed,
		AezeedPassphrase:   aezeedPass,
		RecoveryWindow:     0,
		ChannelBackups:     chanBackups,
		StatelessInit:      statelessInit,
	}
	initWalletResponce, errr := u.InitWallet(ctx, initWalletRequest)
	if errr != nil {
		log.Errorf("Failed to initialize wallet.")
		return response, errr
	}
	if req.StatelessInitFlag {
		if req.SaveTo != "" {
			macSavePath := lncfg.CleanAndExpandPath(req.SaveTo)
			errr := ioutil.WriteFile(macSavePath, initWalletResponce.AdminMacaroon, 0644)
			if errr != nil {
				_ = os.Remove(macSavePath)
				return response, errr
			}
		}
	}
	response = &lnrpc.CreateWalletResponse{
		Seed: cipherSeed,
	}
	return response, nil
}
