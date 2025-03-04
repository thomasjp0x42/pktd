// Copyright (c) 2013-2017 The btcsuite developers
// Copyright (c) 2015-2016 The Decred developers
// Copyright (C) 2015-2017 The Lightning Network Developers

package lnd

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	_ "net/http/pprof" // Blank import to set up profiling HTTP handlers.
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	proxy "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/pkt-cash/pktd/btcutil"
	"github.com/pkt-cash/pktd/btcutil/er"
	"github.com/pkt-cash/pktd/chaincfg/chainhash"
	"github.com/pkt-cash/pktd/neutrino"
	"github.com/pkt-cash/pktd/neutrino/headerfs"
	"github.com/pkt-cash/pktd/pktconfig/version"
	"github.com/pkt-cash/pktd/pktlog/log"
	"github.com/pkt-cash/pktd/pktwallet/wallet"
	"github.com/pkt-cash/pktd/pktwallet/walletdb"
	"google.golang.org/grpc"
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon.v2"

	"github.com/pkt-cash/pktd/lnd/autopilot"
	"github.com/pkt-cash/pktd/lnd/chainreg"
	"github.com/pkt-cash/pktd/lnd/chanacceptor"
	"github.com/pkt-cash/pktd/lnd/channeldb"
	"github.com/pkt-cash/pktd/lnd/keychain"
	"github.com/pkt-cash/pktd/lnd/lncfg"
	"github.com/pkt-cash/pktd/lnd/lnrpc"
	"github.com/pkt-cash/pktd/lnd/lnwallet"
	"github.com/pkt-cash/pktd/lnd/macaroons"
	"github.com/pkt-cash/pktd/lnd/metaservice"
	"github.com/pkt-cash/pktd/lnd/signal"
	"github.com/pkt-cash/pktd/lnd/tor"
	"github.com/pkt-cash/pktd/lnd/walletunlocker"
	"github.com/pkt-cash/pktd/lnd/watchtower"
	"github.com/pkt-cash/pktd/lnd/watchtower/wtdb"
)

// WalletUnlockerAuthOptions returns a list of DialOptions that can be used to
// authenticate with the wallet unlocker service.
//
// NOTE: This should only be called after the WalletUnlocker listener has
// signaled it is ready.
func WalletUnlockerAuthOptions(cfg *Config) ([]grpc.DialOption, er.R) {
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
	}

	return opts, nil
}

// AdminAuthOptions returns a list of DialOptions that can be used to
// authenticate with the RPC server with admin capabilities.
//
// NOTE: This should only be called after the RPCListener has signaled it is
// ready.
func AdminAuthOptions(cfg *Config) ([]grpc.DialOption, er.R) {

	// Create a dial options array.
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
	}

	// Get the admin macaroon if macaroons are active.
	if !cfg.NoMacaroons {
		// Load the adming macaroon file.
		macBytes, err := ioutil.ReadFile(cfg.AdminMacPath)
		if err != nil {
			return nil, er.Errorf("unable to read macaroon "+
				"path (check the network setting!): %v", err)
		}

		mac := &macaroon.Macaroon{}
		if err = mac.UnmarshalBinary(macBytes); err != nil {
			return nil, er.Errorf("unable to decode macaroon: %v",
				err)
		}

		// Now we append the macaroon credentials to the dial options.
		cred := macaroons.NewMacaroonCredential(mac)
		opts = append(opts, grpc.WithPerRPCCredentials(cred))
	}

	return opts, nil
}

// GrpcRegistrar is an interface that must be satisfied by an external subserver
// that wants to be able to register its own gRPC server onto lnd's main
// grpc.Server instance.
type GrpcRegistrar interface {
	// RegisterGrpcSubserver is called for each net.Listener on which lnd
	// creates a grpc.Server instance. External subservers implementing this
	// method can then register their own gRPC server structs to the main
	// server instance.
	RegisterGrpcSubserver(*grpc.Server) er.R
}

// RestRegistrar is an interface that must be satisfied by an external subserver
// that wants to be able to register its own REST mux onto lnd's main
// proxy.ServeMux instance.
type RestRegistrar interface {
	// RegisterRestSubserver is called after lnd creates the main
	// proxy.ServeMux instance. External subservers implementing this method
	// can then register their own REST proxy stubs to the main server
	// instance.
	RegisterRestSubserver(context.Context, *proxy.ServeMux, string,
		[]grpc.DialOption) er.R
}

// RPCSubserverConfig is a struct that can be used to register an external
// subserver with the custom permissions that map to the gRPC server that is
// going to be registered with the GrpcRegistrar.
type RPCSubserverConfig struct {
	// Registrar is a callback that is invoked for each net.Listener on
	// which lnd creates a grpc.Server instance.
	Registrar GrpcRegistrar

	// Permissions is the permissions required for the external subserver.
	// It is a map between the full HTTP URI of each RPC and its required
	// macaroon permissions. If multiple action/entity tuples are specified
	// per URI, they are all required. See rpcserver.go for a list of valid
	// action and entity values.
	Permissions map[string][]bakery.Op

	// MacaroonValidator is a custom macaroon validator that should be used
	// instead of the default lnd validator. If specified, the custom
	// validator is used for all URIs specified in the above Permissions
	// map.
	MacaroonValidator macaroons.MacaroonValidator
}

// ListenerWithSignal is a net.Listener that has an additional Ready channel that
// will be closed when a server starts listening.
type ListenerWithSignal struct {
	net.Listener

	// Ready will be closed by the server listening on Listener.
	Ready chan struct{}

	// ExternalRPCSubserverCfg is optional and specifies the registration
	// callback and permissions to register external gRPC subservers.
	ExternalRPCSubserverCfg *RPCSubserverConfig

	// ExternalRestRegistrar is optional and specifies the registration
	// callback to register external REST subservers.
	ExternalRestRegistrar RestRegistrar
}

// ListenerCfg is a wrapper around custom listeners that can be passed to lnd
// when calling its main method.
type ListenerCfg struct {
	// WalletUnlocker can be set to the listener to use for the wallet
	// unlocker. If nil a regular network listener will be created.
	WalletUnlocker *ListenerWithSignal

	// RPCListener can be set to the listener to use for the RPC server. If
	// nil a regular network listener will be created.
	RPCListener *ListenerWithSignal
}

// rpcListeners is a function type used for closures that fetches a set of RPC
// listeners for the current configuration. If no custom listeners are present,
// this should return normal listeners from the RPC endpoints defined in the
// config. The second return value us a closure that will close the fetched
// listeners.
type rpcListeners func() ([]*ListenerWithSignal, func(), er.R)

// Main is the true entry point for lnd. It accepts a fully populated and
// validated main configuration struct and an optional listener config struct.
// This function starts all main system components then blocks until a signal
// is received on the shutdownChan at which point everything is shut down again.
func Main(cfg *Config, lisCfg ListenerCfg, shutdownChan <-chan struct{}) er.R {
	// Show version at startup.
	log.Infof("Version: %s debuglevel=%s",
		version.Version(), cfg.DebugLevel)

	var network string
	switch {
	case cfg.Bitcoin.TestNet3 || cfg.Litecoin.TestNet3:
		network = "testnet"

	case cfg.Bitcoin.MainNet || cfg.Litecoin.MainNet || cfg.Pkt.MainNet:
		network = "mainnet"

	case cfg.Bitcoin.SimNet || cfg.Litecoin.SimNet:
		network = "simnet"

	case cfg.Bitcoin.RegTest || cfg.Litecoin.RegTest:
		network = "regtest"
	}

	log.Infof("Active chain: %v (network=%v)",
		strings.Title(cfg.registeredChains.PrimaryChain().String()),
		network,
	)

	// Enable http profiling server if requested.
	if cfg.Profile != "" {
		go func() {
			listenAddr := net.JoinHostPort("", cfg.Profile)
			profileRedirect := http.RedirectHandler("/debug/pprof",
				http.StatusSeeOther)
			http.Handle("/", profileRedirect)
			fmt.Println(http.ListenAndServe(listenAddr, nil))
		}()
	}

	// Write cpu profile if requested.
	if cfg.CPUProfile != "" {
		f, err := os.Create(cfg.CPUProfile)
		if err != nil {
			err := er.Errorf("unable to create CPU profile: %v",
				err)
			log.Error(err)
			return err
		}
		pprof.StartCPUProfile(f)
		defer f.Close()
		defer pprof.StopCPUProfile()
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	localChanDB, remoteChanDB, cleanUp, err := initializeDatabases(ctx, cfg)
	switch {
	case channeldb.ErrDryRunMigrationOK.Is(err):
		log.Infof("%v, exiting", err)
		return nil
	case err != nil:
		return er.Errorf("unable to open databases: %v", err)
	}

	defer cleanUp()

	// get gRPC and REST Options (without TLS)
	serverOpts, restDialOpts, restListen := getRpcRestConfig(cfg)

	// We use the first RPC listener as the destination for our REST proxy.
	// If the listener is set to listen on all interfaces, we replace it
	// with localhost, as we cannot dial it directly.
	restProxyDest := cfg.RPCListeners[0].String()
	switch {
	case strings.Contains(restProxyDest, "0.0.0.0"):
		restProxyDest = strings.Replace(
			restProxyDest, "0.0.0.0", "127.0.0.1", 1,
		)

	case strings.Contains(restProxyDest, "[::]"):
		restProxyDest = strings.Replace(
			restProxyDest, "[::]", "[::1]", 1,
		)
	}

	// Before starting the wallet, we'll create and start our Neutrino
	// light client instance, if enabled, in order to allow it to sync
	// while the rest of the daemon continues startup.
	mainChain := cfg.Bitcoin
	if cfg.registeredChains.PrimaryChain() == chainreg.LitecoinChain {
		mainChain = cfg.Litecoin
	}
	if cfg.registeredChains.PrimaryChain() == chainreg.PktChain {
		mainChain = cfg.Pkt
	}
	var neutrinoCS *neutrino.ChainService
	if mainChain.Node == "neutrino" {
		neutrinoBackend, neutrinoCleanUp, err := initNeutrinoBackend(
			cfg, mainChain.ChainDir,
		)
		if err != nil {
			err := er.Errorf("unable to initialize neutrino "+
				"backend: %v", err)
			log.Error(err)
			return err
		}
		defer neutrinoCleanUp()
		neutrinoCS = neutrinoBackend
	}

	var (
		walletInitParams WalletUnlockParams
		shutdownUnlocker = func() {}
		privateWalletPw  = lnwallet.DefaultPrivatePassphrase
		publicWalletPw   = lnwallet.DefaultPublicPassphrase
	)

	// If the user didn't request a seed, then we'll manually assume a
	// wallet birthday of now, as otherwise the seed would've specified
	// this information.
	walletInitParams.Birthday = time.Now()

	// getListeners is a closure that creates listeners from the
	// RPCListeners defined in the config. It also returns a cleanup
	// closure and the server options to use for the GRPC server.
	getListeners := func() ([]*ListenerWithSignal, func(), er.R) {
		var grpcListeners []*ListenerWithSignal
		for _, grpcEndpoint := range cfg.RPCListeners {
			// Start a gRPC server listening for HTTP/2
			// connections.
			lis, err := lncfg.ListenOnAddress(grpcEndpoint)
			if err != nil {
				log.Errorf("unable to listen on %s",
					grpcEndpoint)
				return nil, nil, err
			}
			grpcListeners = append(
				grpcListeners, &ListenerWithSignal{
					Listener: lis,
					Ready:    make(chan struct{}),
				})
		}

		cleanup := func() {
			for _, lis := range grpcListeners {
				lis.Close()
			}
		}
		return grpcListeners, cleanup, nil
	}

	// walletUnlockerListeners is a closure we'll hand to the wallet
	// unlocker, that will be called when it needs listeners for its GPRC
	// server.
	walletUnlockerListeners := func() ([]*ListenerWithSignal, func(),
		er.R) {

		// If we have chosen to start with a dedicated listener for the
		// wallet unlocker, we return it directly.
		if lisCfg.WalletUnlocker != nil {
			return []*ListenerWithSignal{lisCfg.WalletUnlocker},
				func() {}, nil
		}

		// Otherwise we'll return the regular listeners.
		return getListeners()
	}

	// Set up meta Service pass neutrino for getinfo
	metaService := metaservice.NewMetaService(neutrinoCS)

	// We wait until the user provides a password over RPC. In case lnd is
	// started with the --noseedbackup flag, we use the default password
	// for wallet encryption.

	if !cfg.NoSeedBackup {
		params, shutdown, err := waitForWalletPassword(
			cfg, cfg.RESTListeners, serverOpts, restDialOpts,
			restProxyDest, restListen, walletUnlockerListeners, metaService,
		)
		if err != nil {
			err := er.Errorf("unable to set up wallet password "+
				"listeners: %v", err)
			log.Error(err)
			return err
		}

		walletInitParams = *params
		shutdownUnlocker = shutdown
		privateWalletPw = walletInitParams.Password
		publicWalletPw = walletInitParams.Password
		//Pass wallet to metaservice for getinfo2
		metaService.SetWallet(walletInitParams.Wallet)
		defer func() {
			if err := walletInitParams.UnloadWallet(); err != nil {
				log.Errorf("Could not unload wallet: %v", err)
			}
		}()

		if walletInitParams.RecoveryWindow > 0 {
			log.Infof("Wallet recovery mode enabled with "+
				"address lookahead of %d addresses",
				walletInitParams.RecoveryWindow)
		}
	}

	var macaroonService *macaroons.Service
	if !cfg.NoMacaroons {
		// Create the macaroon authentication/authorization service.
		macaroonService, err = macaroons.NewService(
			cfg.networkDir, "lnd", walletInitParams.StatelessInit,
			macaroons.IPLockChecker,
		)
		if err != nil {
			err := er.Errorf("unable to set up macaroon "+
				"authentication: %v", err)
			log.Error(err)
			return err
		}
		defer macaroonService.Close()

		// Try to unlock the macaroon store with the private password.
		// Ignore ErrAlreadyUnlocked since it could be unlocked by the
		// wallet unlocker.
		err = macaroonService.CreateUnlock(&privateWalletPw)
		if err != nil && !macaroons.ErrAlreadyUnlocked.Is(err) {
			err := er.Errorf("unable to unlock macaroons: %v", err)
			log.Error(err)
			return err
		}

		// In case we actually needed to unlock the wallet, we now need
		// to create an instance of the admin macaroon and send it to
		// the unlocker so it can forward it to the user. In no seed
		// backup mode, there's nobody listening on the channel and we'd
		// block here forever.
		if !cfg.NoSeedBackup {
			adminMacBytes, err := bakeMacaroon(
				ctx, macaroonService, adminPermissions(),
			)
			if err != nil {
				return err
			}

			// The channel is buffered by one element so writing
			// should not block here.
			walletInitParams.MacResponseChan <- adminMacBytes
		}

		// If the user requested a stateless initialization, no macaroon
		// files should be created.
		if !walletInitParams.StatelessInit &&
			!fileExists(cfg.AdminMacPath) &&
			!fileExists(cfg.ReadMacPath) &&
			!fileExists(cfg.InvoiceMacPath) {

			// Create macaroon files for lncli to use if they don't
			// exist.
			err = genMacaroons(
				ctx, macaroonService, cfg.AdminMacPath,
				cfg.ReadMacPath, cfg.InvoiceMacPath,
			)
			if err != nil {
				err := er.Errorf("unable to create macaroons "+
					"%v", err)
				log.Error(err)
				return err
			}
		}

		// As a security service to the user, if they requested
		// stateless initialization and there are macaroon files on disk
		// we log a warning.
		if walletInitParams.StatelessInit {
			msg := "Found %s macaroon on disk (%s) even though " +
				"--stateless_init was requested. Unencrypted " +
				"state is accessible by the host system. You " +
				"should change the password and use " +
				"--new_mac_root_key with --stateless_init to " +
				"clean up and invalidate old macaroons."

			if fileExists(cfg.AdminMacPath) {
				log.Warnf(msg, "admin", cfg.AdminMacPath)
			}
			if fileExists(cfg.ReadMacPath) {
				log.Warnf(msg, "readonly", cfg.ReadMacPath)
			}
			if fileExists(cfg.InvoiceMacPath) {
				log.Warnf(msg, "invoice", cfg.InvoiceMacPath)
			}
		}
	}

	// Now we're definitely done with the unlocker, shut it down so we can
	// start the main RPC service later.
	shutdownUnlocker()

	// With the information parsed from the configuration, create valid
	// instances of the pertinent interfaces required to operate the
	// Lightning Network Daemon.
	//
	// When we create the chain control, we need storage for the height
	// hints and also the wallet itself, for these two we want them to be
	// replicated, so we'll pass in the remote channel DB instance.
	chainControlCfg := &chainreg.Config{
		Bitcoin:                     cfg.Bitcoin,
		Litecoin:                    cfg.Litecoin,
		Pkt:                         cfg.Pkt,
		PrimaryChain:                cfg.registeredChains.PrimaryChain,
		HeightHintCacheQueryDisable: cfg.HeightHintCacheQueryDisable,
		NeutrinoMode:                cfg.NeutrinoMode,
		BitcoindMode:                cfg.BitcoindMode,
		LitecoindMode:               cfg.LitecoindMode,
		BtcdMode:                    cfg.BtcdMode,
		LtcdMode:                    cfg.LtcdMode,
		LocalChanDB:                 localChanDB,
		RemoteChanDB:                remoteChanDB,
		PrivateWalletPw:             privateWalletPw,
		PublicWalletPw:              publicWalletPw,
		Birthday:                    walletInitParams.Birthday,
		RecoveryWindow:              walletInitParams.RecoveryWindow,
		Wallet:                      walletInitParams.Wallet,
		NeutrinoCS:                  neutrinoCS,
		ActiveNetParams:             cfg.ActiveNetParams,
		FeeURL:                      cfg.FeeURL,
	}

	activeChainControl, err := chainreg.NewChainControl(chainControlCfg)
	if err != nil {
		err := er.Errorf("unable to create chain control: %v", err)
		log.Error(err)
		return err
	}

	// Finally before we start the server, we'll register the "holy
	// trinity" of interface for our current "home chain" with the active
	// chainRegistry interface.
	primaryChain := cfg.registeredChains.PrimaryChain()
	cfg.registeredChains.RegisterChain(primaryChain, activeChainControl)

	// TODO(roasbeef): add rotation
	idKeyDesc, err := activeChainControl.KeyRing.DeriveKey(
		keychain.KeyLocator{
			Family: keychain.KeyFamilyNodeKey,
			Index:  0,
		},
	)
	if err != nil {
		err := er.Errorf("error deriving node key: %v", err)
		log.Error(err)
		return err
	}

	if cfg.Tor.Active {
		log.Infof("Proxying all network traffic via Tor "+
			"(stream_isolation=%v)! NOTE: Ensure the backend node "+
			"is proxying over Tor as well", cfg.Tor.StreamIsolation)
	}

	// If the watchtower client should be active, open the client database.
	// This is done here so that Close always executes when lndMain returns.
	var towerClientDB *wtdb.ClientDB
	if cfg.WtClient.Active {
		var err er.R
		towerClientDB, err = wtdb.OpenClientDB(cfg.localDatabaseDir())
		if err != nil {
			err := er.Errorf("unable to open watchtower client "+
				"database: %v", err)
			log.Error(err)
			return err
		}
		defer towerClientDB.Close()
	}

	// If tor is active and either v2 or v3 onion services have been specified,
	// make a tor controller and pass it into both the watchtower server and
	// the regular lnd server.
	var torController *tor.Controller
	if cfg.Tor.Active && (cfg.Tor.V2 || cfg.Tor.V3) {
		torController = tor.NewController(
			cfg.Tor.Control, cfg.Tor.TargetIPAddress, cfg.Tor.Password,
		)

		// Start the tor controller before giving it to any other subsystems.
		if err := torController.Start(); err != nil {
			err := er.Errorf("unable to initialize tor controller: %v", err)
			log.Error(err)
			return err
		}
		defer func() {
			if err := torController.Stop(); err != nil {
				log.Errorf("error stopping tor controller: %v", err)
			}
		}()
	}

	var tower *watchtower.Standalone
	if cfg.Watchtower.Active {
		// Segment the watchtower directory by chain and network.
		towerDBDir := filepath.Join(
			cfg.Watchtower.TowerDir,
			cfg.registeredChains.PrimaryChain().String(),
			lncfg.NormalizeNetwork(cfg.ActiveNetParams.Name),
		)

		towerDB, err := wtdb.OpenTowerDB(towerDBDir)
		if err != nil {
			err := er.Errorf("unable to open watchtower "+
				"database: %v", err)
			log.Error(err)
			return err
		}
		defer towerDB.Close()

		towerKeyDesc, err := activeChainControl.KeyRing.DeriveKey(
			keychain.KeyLocator{
				Family: keychain.KeyFamilyTowerID,
				Index:  0,
			},
		)
		if err != nil {
			err := er.Errorf("error deriving tower key: %v", err)
			log.Error(err)
			return err
		}

		wtCfg := &watchtower.Config{
			BlockFetcher:   activeChainControl.ChainIO,
			DB:             towerDB,
			EpochRegistrar: activeChainControl.ChainNotifier,
			Net:            cfg.net,
			NewAddress: func() (btcutil.Address, er.R) {
				return activeChainControl.Wallet.NewAddress(
					lnwallet.WitnessPubKey, false,
				)
			},
			NodeKeyECDH: keychain.NewPubKeyECDH(
				towerKeyDesc, activeChainControl.KeyRing,
			),
			PublishTx: activeChainControl.Wallet.PublishTransaction,
			ChainHash: *cfg.ActiveNetParams.GenesisHash,
		}

		// If there is a tor controller (user wants auto hidden services), then
		// store a pointer in the watchtower config.
		if torController != nil {
			wtCfg.TorController = torController
			wtCfg.WatchtowerKeyPath = cfg.Tor.WatchtowerKeyPath

			switch {
			case cfg.Tor.V2:
				wtCfg.Type = tor.V2
			case cfg.Tor.V3:
				wtCfg.Type = tor.V3
			}
		}

		wtConfig, err := cfg.Watchtower.Apply(wtCfg, lncfg.NormalizeAddresses)
		if err != nil {
			err := er.Errorf("unable to configure watchtower: %v",
				err)
			log.Error(err)
			return err
		}

		tower, err = watchtower.New(wtConfig)
		if err != nil {
			err := er.Errorf("unable to create watchtower: %v", err)
			log.Error(err)
			return err
		}
	}

	// Initialize the ChainedAcceptor.
	chainedAcceptor := chanacceptor.NewChainedAcceptor()

	// Set up the core server which will listen for incoming peer
	// connections.
	server, err := newServer(
		cfg, cfg.Listeners, localChanDB, remoteChanDB, towerClientDB,
		activeChainControl, &idKeyDesc, walletInitParams.ChansToRestore,
		chainedAcceptor, torController,
	)
	if err != nil {
		err := er.Errorf("unable to create server: %v", err)
		log.Error(err)
		return err
	}

	// Set up an autopilot manager from the current config. This will be
	// used to manage the underlying autopilot agent, starting and stopping
	// it at will.
	atplCfg, err := initAutoPilot(server, cfg.Autopilot, mainChain, cfg.ActiveNetParams)
	if err != nil {
		err := er.Errorf("unable to initialize autopilot: %v", err)
		log.Error(err)
		return err
	}

	atplManager, err := autopilot.NewManager(atplCfg)
	if err != nil {
		err := er.Errorf("unable to create autopilot manager: %v", err)
		log.Error(err)
		return err
	}
	if err := atplManager.Start(); err != nil {
		err := er.Errorf("unable to start autopilot manager: %v", err)
		log.Error(err)
		return err
	}
	defer atplManager.Stop()

	// rpcListeners is a closure we'll hand to the rpc server, that will be
	// called when it needs listeners for its GPRC server.
	rpcListeners := func() ([]*ListenerWithSignal, func(), er.R) {
		// If we have chosen to start with a dedicated listener for the
		// rpc server, we return it directly.
		if lisCfg.RPCListener != nil {
			return []*ListenerWithSignal{lisCfg.RPCListener},
				func() {}, nil
		}

		// Otherwise we'll return the regular listeners.
		return getListeners()
	}

	// Initialize, and register our implementation of the gRPC interface
	// exported by the rpcServer.
	rpcServer, err := newRPCServer(
		cfg, server, macaroonService, cfg.SubRPCServers, serverOpts,
		restDialOpts, restProxyDest, atplManager, server.invoices,
		tower, restListen, rpcListeners, chainedAcceptor, metaService,
	)
	if err != nil {
		err := er.Errorf("unable to create RPC server: %v", err)
		log.Error(err)
		return err
	}
	if err := rpcServer.Start(); err != nil {
		err := er.Errorf("unable to start RPC server: %v", err)
		log.Error(err)
		return err
	}
	defer rpcServer.Stop()

	// If we're not in regtest or simnet mode, We'll wait until we're fully
	// synced to continue the start up of the remainder of the daemon. This
	// ensures that we don't accept any possibly invalid state transitions, or
	// accept channels with spent funds.
	if !(cfg.Bitcoin.RegTest || cfg.Bitcoin.SimNet ||
		cfg.Litecoin.RegTest || cfg.Litecoin.SimNet) {

		_, bestHeight, err := activeChainControl.ChainIO.GetBestBlock()
		if err != nil {
			err := er.Errorf("unable to determine chain tip: %v",
				err)
			log.Error(err)
			return err
		}

		log.Infof("Waiting for chain backend to finish sync, "+
			"start_height=%v", bestHeight)

		for {
			if !signal.Alive() {
				return nil
			}

			synced, _, err := activeChainControl.Wallet.IsSynced()
			if err != nil {
				err := er.Errorf("unable to determine if "+
					"wallet is synced: %v", err)
				log.Error(err)
				return err
			}

			if synced {
				break
			}

			time.Sleep(time.Second * 1)
		}

		_, bestHeight, err = activeChainControl.ChainIO.GetBestBlock()
		if err != nil {
			err := er.Errorf("unable to determine chain tip: %v",
				err)
			log.Error(err)
			return err
		}

		log.Infof("Chain backend is fully synced (end_height=%v)!",
			bestHeight)
	}

	// With all the relevant chains initialized, we can finally start the
	// server itself.
	if err := server.Start(); err != nil {
		err := er.Errorf("unable to start server: %v", err)
		log.Error(err)
		return err
	}
	defer server.Stop()

	// Now that the server has started, if the autopilot mode is currently
	// active, then we'll start the autopilot agent immediately. It will be
	// stopped together with the autopilot service.
	if cfg.Autopilot.Active {
		if err := atplManager.StartAgent(); err != nil {
			err := er.Errorf("unable to start autopilot agent: %v",
				err)
			log.Error(err)
			return err
		}
	}

	if cfg.Watchtower.Active {
		if err := tower.Start(); err != nil {
			err := er.Errorf("unable to start watchtower: %v", err)
			log.Error(err)
			return err
		}
		defer tower.Stop()
	}

	// Wait for shutdown signal from either a graceful server stop or from
	// the interrupt handler.
	<-shutdownChan
	return nil
}

func getRpcRestConfig(cfg *Config) ([]grpc.ServerOption, []grpc.DialOption, func(net.Addr) (net.Listener, er.R)) {

	// For our REST dial options, we'll still use TLS, but also increase
	// the max message size that we'll decode to allow clients to hit
	// endpoints which return more data such as the DescribeGraph call.
	// We set this to 200MiB atm. Should be the same value as maxMsgRecvSize
	// in cmd/lncli/main.go.
	restDialOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(1 * 1024 * 1024 * 200),
		),
	}

	var serverOpts []grpc.ServerOption
	restListen := func(addr net.Addr) (net.Listener, er.R) {
		return lncfg.ListenOnAddress(addr)
	}

	return serverOpts, restDialOpts, restListen
}

// fileExists reports whether the named file or directory exists.
// This function is taken from https://github.com/btcsuite/btcd
func fileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// bakeMacaroon creates a new macaroon with newest version and the given
// permissions then returns it binary serialized.
func bakeMacaroon(ctx context.Context, svc *macaroons.Service,
	permissions []bakery.Op) ([]byte, er.R) {

	mac, err := svc.NewMacaroon(
		ctx, macaroons.DefaultRootKeyID, permissions...,
	)
	if err != nil {
		return nil, err
	}

	b, e := mac.M().MarshalBinary()
	return b, er.E(e)
}

// genMacaroons generates three macaroon files; one admin-level, one for
// invoice access and one read-only. These can also be used to generate more
// granular macaroons.
func genMacaroons(ctx context.Context, svc *macaroons.Service,
	admFile, roFile, invoiceFile string) er.R {

	// First, we'll generate a macaroon that only allows the caller to
	// access invoice related calls. This is useful for merchants and other
	// services to allow an isolated instance that can only query and
	// modify invoices.
	invoiceMacBytes, err := bakeMacaroon(ctx, svc, invoicePermissions)
	if err != nil {
		return err
	}
	errr := ioutil.WriteFile(invoiceFile, invoiceMacBytes, 0644)
	if errr != nil {
		_ = os.Remove(invoiceFile)
		return er.E(errr)
	}

	// Generate the read-only macaroon and write it to a file.
	roBytes, err := bakeMacaroon(ctx, svc, readPermissions)
	if err != nil {
		return err
	}
	if errr = ioutil.WriteFile(roFile, roBytes, 0644); errr != nil {
		_ = os.Remove(roFile)
		return er.E(errr)
	}

	// Generate the admin macaroon and write it to a file.
	admBytes, err := bakeMacaroon(ctx, svc, adminPermissions())
	if err != nil {
		return err
	}
	if errr = ioutil.WriteFile(admFile, admBytes, 0600); errr != nil {
		_ = os.Remove(admFile)
		return er.E(errr)
	}

	return nil
}

// adminPermissions returns a list of all permissions in a safe way that doesn't
// modify any of the source lists.
func adminPermissions() []bakery.Op {
	admin := make([]bakery.Op, len(readPermissions)+len(writePermissions))
	copy(admin[:len(readPermissions)], readPermissions)
	copy(admin[len(readPermissions):], writePermissions)
	return admin
}

// WalletUnlockParams holds the variables used to parameterize the unlocking of
// lnd's wallet after it has already been created.
type WalletUnlockParams struct {
	// Password is the public and private wallet passphrase.
	Password []byte

	// Birthday specifies the approximate time that this wallet was created.
	// This is used to bound any rescans on startup.
	Birthday time.Time

	// RecoveryWindow specifies the address lookahead when entering recovery
	// mode. A recovery will be attempted if this value is non-zero.
	RecoveryWindow uint32

	// Wallet is the loaded and unlocked Wallet. This is returned
	// from the unlocker service to avoid it being unlocked twice (once in
	// the unlocker service to check if the password is correct and again
	// later when lnd actually uses it). Because unlocking involves scrypt
	// which is resource intensive, we want to avoid doing it twice.
	Wallet *wallet.Wallet

	// ChansToRestore a set of static channel backups that should be
	// restored before the main server instance starts up.
	ChansToRestore walletunlocker.ChannelsToRecover

	// UnloadWallet is a function for unloading the wallet, which should
	// be called on shutdown.
	UnloadWallet func() er.R

	// StatelessInit signals that the user requested the daemon to be
	// initialized stateless, which means no unencrypted macaroons should be
	// written to disk.
	StatelessInit bool

	// MacResponseChan is the channel for sending back the admin macaroon to
	// the WalletUnlocker service.
	MacResponseChan chan []byte
}

// waitForWalletPassword will spin up gRPC and REST endpoints for the
// WalletUnlocker server, and block until a password is provided by
// the user to this RPC server.
func waitForWalletPassword(cfg *Config, restEndpoints []net.Addr,
	serverOpts []grpc.ServerOption, restDialOpts []grpc.DialOption,
	restProxyDest string, restListen func(net.Addr) (net.Listener, er.R),
	getListeners rpcListeners, metaService *metaservice.MetaService) (*WalletUnlockParams, func(), er.R) {

	chainConfig := cfg.Bitcoin
	if cfg.registeredChains.PrimaryChain() == chainreg.LitecoinChain {
		chainConfig = cfg.Litecoin
	} else if cfg.registeredChains.PrimaryChain() == chainreg.PktChain {
		chainConfig = cfg.Pkt
	}

	// The macaroonFiles are passed to the wallet unlocker so they can be
	// deleted and recreated in case the root macaroon key is also changed
	// during the change password operation.
	macaroonFiles := []string{
		cfg.AdminMacPath, cfg.ReadMacPath, cfg.InvoiceMacPath,
	}
	//Parse filename from --wallet or default
	walletPath, walletFilename := WalletFilename(cfg.WalletFile)
	//Get default pkt dir ~/.pktwallet/pkt
	if walletPath == "" {
		walletPath = cfg.Pktmode.WalletDir
	}
	pwService := walletunlocker.New(
		chainConfig.ChainDir, cfg.ActiveNetParams.Params,
		!cfg.SyncFreelist, macaroonFiles, walletPath, walletFilename,
	)

	// Set up a new PasswordService, which will listen for passwords
	// provided over RPC.
	grpcServer := grpc.NewServer(serverOpts...)
	lnrpc.RegisterWalletUnlockerServer(grpcServer, pwService)
	// Set up metaservice allowing getinfo to work even when wallet is locked
	lnrpc.RegisterMetaServiceServer(grpcServer, metaService)

	var shutdownFuncs []func()
	shutdown := func() {
		// Make sure nothing blocks on reading on the macaroon channel,
		// otherwise the GracefulStop below will never return.
		close(pwService.MacResponseChan)

		for _, shutdownFn := range shutdownFuncs {
			shutdownFn()
		}
	}
	shutdownFuncs = append(shutdownFuncs, grpcServer.GracefulStop)

	// Start a gRPC server listening for HTTP/2 connections, solely used
	// for getting the encryption password from the client.
	listeners, cleanup, err := getListeners()
	if err != nil {
		return nil, shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, cleanup)

	// Use a WaitGroup so we can be sure the instructions on how to input the
	// password is the last thing to be printed to the console.
	var wg sync.WaitGroup

	for _, lis := range listeners {
		wg.Add(1)
		go func(lis *ListenerWithSignal) {
			log.Infof("Password RPC server listening on %s",
				lis.Addr())

			// Close the ready chan to indicate we are listening.
			close(lis.Ready)

			wg.Done()
			_ = grpcServer.Serve(lis)
		}(lis)
	}

	// Start a REST proxy for our gRPC server above.
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	shutdownFuncs = append(shutdownFuncs, cancel)

	mux := proxy.NewServeMux()

	errr := lnrpc.RegisterWalletUnlockerHandlerFromEndpoint(
		ctx, mux, restProxyDest, restDialOpts,
	)
	if errr != nil {
		return nil, shutdown, er.E(errr)
	}

	srv := &http.Server{Handler: allowCORS(mux, cfg.RestCORS)}

	for _, restEndpoint := range restEndpoints {
		lis, err := restListen(restEndpoint)
		if err != nil {
			log.Errorf("Password gRPC proxy unable to listen "+
				"on %s", restEndpoint)
			return nil, shutdown, err
		}
		shutdownFuncs = append(shutdownFuncs, func() {
			err := lis.Close()
			if err != nil {
				log.Errorf("Error closing listener: %v",
					err)
			}
		})

		wg.Add(1)
		go func() {
			log.Infof("Password gRPC proxy started at %s",
				lis.Addr())
			wg.Done()
			_ = srv.Serve(lis)
		}()
	}

	// Wait for gRPC and REST servers to be up running.
	wg.Wait()

	// Wait for user to provide the password.
	log.Infof("Waiting for wallet (" + walletFilename + ") encryption password. Use `lncli " +
		"create` to create a wallet, `lncli unlock` to unlock an " +
		"existing wallet, or `lncli changepassword` to change the " +
		"password of an existing wallet and unlock it.")

	// We currently don't distinguish between getting a password to be used
	// for creation or unlocking, as a new wallet db will be created if
	// none exists when creating the chain control.
	select {

	// The wallet is being created for the first time, we'll check to see
	// if the user provided any entropy for seed creation. If so, then
	// we'll create the wallet early to load the seed.
	case initMsg := <-pwService.InitMsgs:
		password := initMsg.Passphrase
		cipherSeed := initMsg.Seed
		recoveryWindow := initMsg.RecoveryWindow

		loader := wallet.NewLoader(
			cfg.ActiveNetParams.Params, walletPath, walletFilename, !cfg.SyncFreelist,
			recoveryWindow,
		)

		newWallet, err := loader.CreateNewWallet(
			nil, password, nil, time.Time{}, cipherSeed,
		)
		if err != nil {
			// Don't leave the file open in case the new wallet
			// could not be created for whatever reason.
			if err := loader.UnloadWallet(); err != nil {
				log.Errorf("Could not unload new "+
					"wallet: %v", err)
			}
			return nil, shutdown, err
		}

		// For new wallets, the ResetWalletTransactions flag is a no-op.
		if cfg.ResetWalletTransactions {
			log.Warnf("Ignoring reset-wallet-transactions " +
				"flag for new wallet as it has no effect")
		}

		return &WalletUnlockParams{
			Password:        password,
			Birthday:        cipherSeed.Birthday(),
			RecoveryWindow:  recoveryWindow,
			Wallet:          newWallet,
			ChansToRestore:  initMsg.ChanBackups,
			UnloadWallet:    loader.UnloadWallet,
			StatelessInit:   initMsg.StatelessInit,
			MacResponseChan: pwService.MacResponseChan,
		}, shutdown, nil

	// The wallet has already been created in the past, and is simply being
	// unlocked. So we'll just return these passphrases.
	case unlockMsg := <-pwService.UnlockMsgs:
		// Resetting the transactions is something the user likely only
		// wants to do once so we add a prominent warning to the log to
		// remind the user to turn off the setting again after
		// successful completion.
		if cfg.ResetWalletTransactions {
			log.Warnf("Dropping all transaction history from " +
				"on-chain wallet. Remember to disable " +
				"reset-wallet-transactions flag for next " +
				"start of lnd")

			err := wallet.DropTransactionHistory(
				unlockMsg.Wallet.Database(), true,
			)
			if err != nil {
				if err := unlockMsg.UnloadWallet(); err != nil {
					log.Errorf("Could not unload "+
						"wallet: %v", err)
				}
				return nil, shutdown, err
			}
		}

		return &WalletUnlockParams{
			Password:        unlockMsg.Passphrase,
			RecoveryWindow:  unlockMsg.RecoveryWindow,
			Wallet:          unlockMsg.Wallet,
			ChansToRestore:  unlockMsg.ChanBackups,
			UnloadWallet:    unlockMsg.UnloadWallet,
			StatelessInit:   unlockMsg.StatelessInit,
			MacResponseChan: pwService.MacResponseChan,
		}, shutdown, nil

	case <-signal.ShutdownChannel():
		return nil, shutdown, er.Errorf("shutting down")
	}
}

// initializeDatabases extracts the current databases that we'll use for normal
// operation in the daemon. Two databases are returned: one remote and one
// local. However, only if the replicated database is active will the remote
// database point to a unique database. Otherwise, the local and remote DB will
// both point to the same local database. A function closure that closes all
// opened databases is also returned.
func initializeDatabases(ctx context.Context,
	cfg *Config) (*channeldb.DB, *channeldb.DB, func(), er.R) {

	log.Infof("Opening the main database, this might take a few " +
		"minutes...")

	if cfg.DB.Backend == lncfg.BoltBackend {
		log.Infof("Opening bbolt database, sync_freelist=%v, "+
			"auto_compact=%v", cfg.DB.Bolt.SyncFreelist,
			cfg.DB.Bolt.AutoCompact)
	}

	startOpenTime := time.Now()

	databaseBackends, err := cfg.DB.GetBackends(
		ctx, cfg.localDatabaseDir(), cfg.networkName(),
	)
	if err != nil {
		return nil, nil, nil, er.Errorf("unable to obtain database "+
			"backends: %v", err)
	}

	// If the remoteDB is nil, then we'll just open a local DB as normal,
	// having the remote and local pointer be the exact same instance.
	var (
		localChanDB, remoteChanDB *channeldb.DB
		closeFuncs                []func()
	)
	if databaseBackends.RemoteDB == nil {
		// Open the channeldb, which is dedicated to storing channel,
		// and network related metadata.
		localChanDB, err = channeldb.CreateWithBackend(
			databaseBackends.LocalDB,
			channeldb.OptionSetRejectCacheSize(cfg.Caches.RejectCacheSize),
			channeldb.OptionSetChannelCacheSize(cfg.Caches.ChannelCacheSize),
			channeldb.OptionDryRunMigration(cfg.DryRunMigration),
		)
		switch {
		case channeldb.ErrDryRunMigrationOK.Is(err):
			return nil, nil, nil, err

		case err != nil:
			err := er.Errorf("unable to open local channeldb: %v", err)
			log.Error(err)
			return nil, nil, nil, err
		}

		closeFuncs = append(closeFuncs, func() {
			localChanDB.Close()
		})

		remoteChanDB = localChanDB
	} else {
		log.Infof("Database replication is available! Creating " +
			"local and remote channeldb instances")

		// Otherwise, we'll open two instances, one for the state we
		// only need locally, and the other for things we want to
		// ensure are replicated.
		localChanDB, err = channeldb.CreateWithBackend(
			databaseBackends.LocalDB,
			channeldb.OptionSetRejectCacheSize(cfg.Caches.RejectCacheSize),
			channeldb.OptionSetChannelCacheSize(cfg.Caches.ChannelCacheSize),
			channeldb.OptionDryRunMigration(cfg.DryRunMigration),
		)
		switch {
		// As we want to allow both versions to get thru the dry run
		// migration, we'll only exit the second time here once the
		// remote instance has had a time to migrate as well.
		case channeldb.ErrDryRunMigrationOK.Is(err):
			log.Infof("Local DB dry run migration successful")

		case err != nil:
			err := er.Errorf("unable to open local channeldb: %v", err)
			log.Error(err)
			return nil, nil, nil, err
		}

		closeFuncs = append(closeFuncs, func() {
			localChanDB.Close()
		})

		log.Infof("Opening replicated database instance...")

		remoteChanDB, err = channeldb.CreateWithBackend(
			databaseBackends.RemoteDB,
			channeldb.OptionDryRunMigration(cfg.DryRunMigration),
		)
		switch {
		case channeldb.ErrDryRunMigrationOK.Is(err):
			return nil, nil, nil, err

		case err != nil:
			localChanDB.Close()

			err := er.Errorf("unable to open remote channeldb: %v", err)
			log.Error(err)
			return nil, nil, nil, err
		}

		closeFuncs = append(closeFuncs, func() {
			remoteChanDB.Close()
		})
	}

	openTime := time.Since(startOpenTime)
	log.Infof("Database now open (time_to_open=%v)!", openTime)

	cleanUp := func() {
		for _, closeFunc := range closeFuncs {
			closeFunc()
		}
	}

	return localChanDB, remoteChanDB, cleanUp, nil
}

// initNeutrinoBackend inits a new instance of the neutrino light client
// backend given a target chain directory to store the chain state.
func initNeutrinoBackend(cfg *Config, chainDir string) (*neutrino.ChainService,
	func(), er.R) {

	// First we'll open the database file for neutrino, creating the
	// database if needed. We append the normalized network name here to
	// match the behavior of btcwallet.
	dbPath := filepath.Join(
		chainDir, lncfg.NormalizeNetwork(cfg.ActiveNetParams.Name),
	)

	// Ensure that the neutrino db path exists.
	if errr := os.MkdirAll(dbPath, 0700); errr != nil {
		return nil, nil, er.E(errr)
	}

	dbName := filepath.Join(dbPath, "neutrino.db")
	db, err := walletdb.Create("bdb", dbName, !cfg.SyncFreelist)
	if err != nil {
		return nil, nil, er.Errorf("unable to create neutrino "+
			"database: %v", err)
	}

	headerStateAssertion, errr := parseHeaderStateAssertion(
		cfg.NeutrinoMode.AssertFilterHeader,
	)
	if errr != nil {
		db.Close()
		return nil, nil, errr
	}

	// With the database open, we can now create an instance of the
	// neutrino light client. We pass in relevant configuration parameters
	// required.
	config := neutrino.Config{
		DataDir:      dbPath,
		Database:     db,
		ChainParams:  *cfg.ActiveNetParams.Params,
		AddPeers:     cfg.NeutrinoMode.AddPeers,
		ConnectPeers: cfg.NeutrinoMode.ConnectPeers,
		Dialer: func(addr net.Addr) (net.Conn, er.R) {
			return cfg.net.Dial(
				addr.Network(), addr.String(),
				cfg.ConnectionTimeout,
			)
		},
		NameResolver: func(host string) ([]net.IP, er.R) {
			addrs, err := cfg.net.LookupHost(host)
			if err != nil {
				return nil, err
			}

			ips := make([]net.IP, 0, len(addrs))
			for _, strIP := range addrs {
				ip := net.ParseIP(strIP)
				if ip == nil {
					continue
				}

				ips = append(ips, ip)
			}

			return ips, nil
		},
		AssertFilterHeader: headerStateAssertion,
	}

	neutrino.MaxPeers = 8
	neutrino.BanDuration = time.Hour * 48
	neutrino.UserAgentName = cfg.NeutrinoMode.UserAgentName
	neutrino.UserAgentVersion = cfg.NeutrinoMode.UserAgentVersion

	neutrinoCS, err := neutrino.NewChainService(config)
	if err != nil {
		db.Close()
		return nil, nil, er.Errorf("unable to create neutrino light "+
			"client: %v", err)
	}

	if err := neutrinoCS.Start(); err != nil {
		db.Close()
		return nil, nil, err
	}

	cleanUp := func() {
		if err := neutrinoCS.Stop(); err != nil {
			log.Infof("Unable to stop neutrino light client: %v", err)
		}
		db.Close()
	}

	return neutrinoCS, cleanUp, nil
}

// parseHeaderStateAssertion parses the user-specified neutrino header state
// into a headerfs.FilterHeader.
func parseHeaderStateAssertion(state string) (*headerfs.FilterHeader, er.R) {
	if len(state) == 0 {
		return nil, nil
	}

	split := strings.Split(state, ":")
	if len(split) != 2 {
		return nil, er.Errorf("header state assertion %v in "+
			"unexpected format, expected format height:hash", state)
	}

	height, errr := strconv.ParseUint(split[0], 10, 32)
	if errr != nil {
		return nil, er.Errorf("invalid filter header height: %v", errr)
	}

	hash, err := chainhash.NewHashFromStr(split[1])
	if err != nil {
		return nil, er.Errorf("invalid filter header hash: %v", err)
	}

	return &headerfs.FilterHeader{
		Height:     uint32(height),
		FilterHash: *hash,
	}, nil
}

// Parse wallet filename,
// return path and filename when it starts with /
func WalletFilename(walletName string) (string, string) {
	if strings.HasSuffix(walletName, ".db") {
		if strings.HasPrefix(walletName, "/") {
			dir, filename := filepath.Split(walletName)
			return dir, filename
		}
		return "", walletName
	} else {
		return "", fmt.Sprintf("wallet_%s.db", walletName)
	}
}
