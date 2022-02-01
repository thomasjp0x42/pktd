package metaservice

import (
	"context"
	"os"
	"strconv"

	"github.com/pkt-cash/pktd/btcjson"
	"github.com/pkt-cash/pktd/btcutil/er"
	"github.com/pkt-cash/pktd/chaincfg"
	"github.com/pkt-cash/pktd/connmgr/banmgr"
	"github.com/pkt-cash/pktd/lnd/lncfg"
	"github.com/pkt-cash/pktd/lnd/lnrpc"
	"github.com/pkt-cash/pktd/lnd/lnwallet"
	"github.com/pkt-cash/pktd/lnd/lnwallet/btcwallet"
	"github.com/pkt-cash/pktd/lnd/macaroons"
	"github.com/pkt-cash/pktd/neutrino"
	"github.com/pkt-cash/pktd/pktlog/log"
	"github.com/pkt-cash/pktd/pktwallet/waddrmgr"
	"github.com/pkt-cash/pktd/pktwallet/wallet"
	"google.golang.org/grpc"
)

type MetaService struct {
	Neutrino *neutrino.ChainService
	Wallet   *wallet.Wallet

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

var _ lnrpc.MetaServiceServer = (*MetaService)(nil)

// New creates and returns a new MetaService
func NewMetaService(neutrino *neutrino.ChainService) *MetaService {
	return &MetaService{
		Neutrino: neutrino,
	}
}

func (m *MetaService) SetWallet(wallet *wallet.Wallet) {
	m.Wallet = wallet
}

func (m *MetaService) Init(MacResponseChan chan []byte, chainDir string,
	noFreelistSync bool, netParams *chaincfg.Params, macaroonFiles []string, walletFile, walletPath string) {
	m.MacResponseChan = MacResponseChan
	m.chainDir = chainDir
	m.netParams = netParams
	m.macaroonFiles = macaroonFiles
	m.walletFile = walletFile
	m.walletPath = walletPath
}

func (m *MetaService) GetInfo2(ctx context.Context,
	in *lnrpc.GetInfo2Request) (*lnrpc.GetInfo2Response, error) {
	res, err := m.GetInfo20(ctx, in)
	return res, er.Native(err)
}

func getClientConn(ctx *context.Context, skipMacaroons bool) *grpc.ClientConn {
	var defaultRPCPort = "10009"
	var maxMsgRecvSize = grpc.MaxCallRecvMsgSize(1 * 1024 * 1024 * 200)
	// First, we'll get the selected stored profile or an ephemeral one
	// created from the global options in the CLI context.

	//profile, err := getGlobalOptions(ctx, true)
	// if err != nil {
	// 	log.Errorf("could not load global options: %v", err)
	// }

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())

	// We need to use a custom dialer so we can also connect to unix sockets
	// and not just TCP addresses.
	genericDialer := lncfg.ClientAddressDialer(defaultRPCPort)
	opts = append(opts, grpc.WithContextDialer(genericDialer))
	opts = append(opts, grpc.WithDefaultCallOptions(maxMsgRecvSize))

	conn, errr := grpc.Dial("localhost:10009", opts...)
	if errr != nil {
		log.Errorf("unable to connect to RPC server: %v", errr)
		return nil
	}

	return conn
}

func getClient(ctx *context.Context) (lnrpc.LightningClient, func()) {
	conn := getClientConn(ctx, false)

	cleanUp := func() {
		conn.Close()
	}

	return lnrpc.NewLightningClient(conn), cleanUp
}

func (m *MetaService) GetInfo20(ctx context.Context,
	in *lnrpc.GetInfo2Request) (*lnrpc.GetInfo2Response, er.R) {

	var ni lnrpc.NeutrinoInfo
	neutrinoPeers := m.Neutrino.Peers()
	for i := range neutrinoPeers {
		var peerDesc lnrpc.PeerDesc
		neutrinoPeer := neutrinoPeers[i]

		peerDesc.BytesReceived = neutrinoPeer.BytesReceived()
		peerDesc.BytesSent = neutrinoPeer.BytesSent()
		peerDesc.LastRecv = neutrinoPeer.LastRecv().String()
		peerDesc.LastSend = neutrinoPeer.LastSend().String()
		peerDesc.Connected = neutrinoPeer.Connected()
		peerDesc.Addr = neutrinoPeer.Addr()
		peerDesc.Inbound = neutrinoPeer.Inbound()
		na := neutrinoPeer.NA()
		if na != nil {
			peerDesc.Na = na.IP.String() + ":" + strconv.Itoa(int(na.Port))
		}
		peerDesc.Id = neutrinoPeer.ID()
		peerDesc.UserAgent = neutrinoPeer.UserAgent()
		peerDesc.Services = neutrinoPeer.Services().String()
		peerDesc.VersionKnown = neutrinoPeer.VersionKnown()
		peerDesc.AdvertisedProtoVer = neutrinoPeer.Describe().AdvertisedProtoVer
		peerDesc.ProtocolVersion = neutrinoPeer.ProtocolVersion()
		peerDesc.SendHeadersPreferred = neutrinoPeer.Describe().SendHeadersPreferred
		peerDesc.VerAckReceived = neutrinoPeer.VerAckReceived()
		peerDesc.WitnessEnabled = neutrinoPeer.Describe().WitnessEnabled
		peerDesc.WireEncoding = strconv.Itoa(int(neutrinoPeer.Describe().WireEncoding))
		peerDesc.TimeOffset = neutrinoPeer.TimeOffset()
		peerDesc.TimeConnected = neutrinoPeer.Describe().TimeConnected.String()
		peerDesc.StartingHeight = neutrinoPeer.StartingHeight()
		peerDesc.LastBlock = neutrinoPeer.LastBlock()
		if neutrinoPeer.LastAnnouncedBlock() != nil {
			peerDesc.LastAnnouncedBlock = neutrinoPeer.LastAnnouncedBlock().CloneBytes()
		}
		peerDesc.LastPingNonce = neutrinoPeer.LastPingNonce()
		peerDesc.LastPingTime = neutrinoPeer.LastPingTime().String()
		peerDesc.LastPingMicros = neutrinoPeer.LastPingMicros()

		ni.Peers = append(ni.Peers, &peerDesc)
	}
	m.Neutrino.BanMgr().ForEachIp(func(bi banmgr.BanInfo) er.R {
		ban := lnrpc.NeutrinoBan{}
		ban.Addr = bi.Addr
		ban.Reason = bi.Reason
		ban.EndTime = bi.BanExpiresTime.String()
		ban.BanScore = bi.BanScore

		ni.Bans = append(ni.Bans, &ban)
		return nil
	})

	neutrionoQueries := m.Neutrino.GetActiveQueries()
	for i := range neutrionoQueries {
		nq := lnrpc.NeutrinoQuery{}
		query := neutrionoQueries[i]
		nq.Peer = query.Peer.String()
		nq.Command = query.Command
		nq.ReqNum = query.ReqNum
		nq.CreateTime = query.CreateTime
		nq.LastRequestTime = query.LastRequestTime
		nq.LastResponseTime = query.LastResponseTime

		ni.Queries = append(ni.Queries, &nq)
	}

	bb, err := m.Neutrino.BestBlock()
	if err != nil {
		return nil, err
	}
	ni.BlockHash = bb.Hash.String()
	ni.Height = bb.Height
	ni.BlockTimestamp = bb.Timestamp.String()
	ni.IsSyncing = !m.Neutrino.IsCurrent()

	mgrStamp := waddrmgr.BlockStamp{}
	walletInfo := &lnrpc.WalletInfo{}

	if m.Wallet != nil {
		mgrStamp = m.Wallet.Manager.SyncedTo()
		walletStats := &lnrpc.WalletStats{}
		m.Wallet.ReadStats(func(ws *btcjson.WalletStats) {
			walletStats.MaintenanceInProgress = ws.MaintenanceInProgress
			walletStats.MaintenanceName = ws.MaintenanceName
			walletStats.MaintenanceCycles = int32(ws.MaintenanceCycles)
			walletStats.MaintenanceLastBlockVisited = int32(ws.MaintenanceLastBlockVisited)
			walletStats.Syncing = ws.Syncing
			if ws.SyncStarted != nil {
				walletStats.SyncStarted = ws.SyncStarted.String()
			}
			walletStats.SyncRemainingSeconds = ws.SyncRemainingSeconds
			walletStats.SyncCurrentBlock = ws.SyncCurrentBlock
			walletStats.SyncFrom = ws.SyncFrom
			walletStats.SyncTo = ws.SyncTo
			walletStats.BirthdayBlock = ws.BirthdayBlock
		})
		walletInfo = &lnrpc.WalletInfo{
			CurrentBlockHash:      mgrStamp.Hash.String(),
			CurrentHeight:         mgrStamp.Height,
			CurrentBlockTimestamp: mgrStamp.Timestamp.String(),
			WalletVersion:         int32(waddrmgr.LatestMgrVersion),
			WalletStats:           walletStats,
		}
	} else {
		walletInfo = nil
	}
	//Get Lightning info
	
	ctxb := context.Background()
	client, cleanUp := getClient(&ctx)
	defer cleanUp()
	inforeq := &lnrpc.GetInfoRequest{}
	inforesp, infoerr := client.GetInfo(ctxb, inforeq)
	if infoerr != nil {
		inforesp = nil
	}

	return &lnrpc.GetInfo2Response{
		Neutrino:  &ni,
		Wallet:    walletInfo,
		Lightning: inforesp,
	}, nil
}

func (u *MetaService) ChangePassword(ctx context.Context,
	in *lnrpc.ChangePasswordRequest) (*lnrpc.ChangePasswordResponse, error) {
	res, err := u.ChangePassword0(ctx, in)
	return res, er.Native(err)
}

// ChangePassword changes the password of the wallet and sends the new password
// across the UnlockPasswords channel to automatically unlock the wallet if
// successful.
func (m *MetaService) ChangePassword0(ctx context.Context,
	in *lnrpc.ChangePasswordRequest) (*lnrpc.ChangePasswordResponse, er.R) {

	privatePw := in.CurrentPassword
	newPubPw := []byte(wallet.InsecurePubPassphrase)
	publicPw := []byte(wallet.InsecurePubPassphrase)
	if in.CurrentPubPassword != nil {
		publicPw = in.CurrentPubPassword
	}

	if in.NewPubPassword != nil {
		newPubPw = in.NewPubPassword
	}
	// If the current password is blank, we'll assume the user is coming
	// from a --noseedbackup state, so we'll use the default passwords.
	if len(in.CurrentPassword) == 0 {
		publicPw = lnwallet.DefaultPublicPassphrase
		privatePw = lnwallet.DefaultPrivatePassphrase
	}

	if m.Wallet == nil || m.Wallet.Locked() {
		loader := wallet.NewLoader(m.netParams, m.walletPath, m.walletFile, m.noFreelistSync, 0)

		// First, we'll make sure the wallet exists for the specific chain and
		// network.
		walletExists, err := loader.WalletExists()
		if err != nil {
			return nil, err
		}

		if !walletExists {
			return nil, er.New("wallet not found")
		}

		// Make sure the new password meets our constraints.
		if err := ValidatePassword(in.NewPassword); err != nil {
			return nil, err
		}
		if in.NewPubPassword != nil {
			if err := ValidatePassword(in.NewPassword); err != nil {
				return nil, err
			}
		}

		// Load the existing wallet in order to proceed with the password change.
		w, err := loader.OpenExistingWallet(publicPw, false)
		if err != nil {
			return nil, err
		}
		m.Wallet = w
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
			for _, file := range m.macaroonFiles {
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
	} //wallet is locked

	// Attempt to change both the public and private passphrases for the
	// wallet. This will be done atomically in order to prevent one
	// passphrase change from being successful and not the other.
	err := m.Wallet.ChangePassphrases(
		publicPw, newPubPw, privatePw, in.NewPassword,
	)
	if err != nil {
		return nil, er.Errorf("unable to change wallet passphrase: "+
			"%v", err)
	}

	adminMac := []byte{}
	// Check if macaroonFiles is populated, if not it's due to noMacaroon flag is set
	//so we do not need the service
	if len(m.macaroonFiles) > 0 {
		netDir := btcwallet.NetworkDir(m.chainDir, m.netParams)
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
		adminMac = <-m.MacResponseChan
	}
	return &lnrpc.ChangePasswordResponse{
		AdminMacaroon: adminMac,
	}, nil
}

// ValidatePassword assures the password meets all of our constraints.
func ValidatePassword(password []byte) er.R {
	// Passwords should have a length of at least 8 characters.
	if len(password) < 8 {
		return er.New("password must have at least 8 characters")
	}

	return nil
}
