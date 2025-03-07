// Copyright 2020 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package routing

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	appserviceAPI "github.com/matrix-org/dendrite/appservice/api"
	"github.com/matrix-org/dendrite/clientapi/api"
	"github.com/matrix-org/dendrite/clientapi/auth"
	clientutil "github.com/matrix-org/dendrite/clientapi/httputil"
	"github.com/matrix-org/dendrite/clientapi/jsonerror"
	"github.com/matrix-org/dendrite/clientapi/producers"
	eduServerAPI "github.com/matrix-org/dendrite/eduserver/api"
	federationAPI "github.com/matrix-org/dendrite/federationapi/api"
	"github.com/matrix-org/dendrite/internal/httputil"
	"github.com/matrix-org/dendrite/internal/transactions"
	keyserverAPI "github.com/matrix-org/dendrite/keyserver/api"
	roomserverAPI "github.com/matrix-org/dendrite/roomserver/api"
	"github.com/matrix-org/dendrite/setup/config"
	userapi "github.com/matrix-org/dendrite/userapi/api"
	"github.com/matrix-org/dendrite/userapi/storage/accounts"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/util"
	"github.com/sirupsen/logrus"
)

// Setup registers HTTP handlers with the given ServeMux. It also supplies the given http.Client
// to clients which need to make outbound HTTP requests.
//
// Due to Setup being used to call many other functions, a gocyclo nolint is
// applied:
// nolint: gocyclo
func Setup(
	publicAPIMux, synapseAdminRouter *mux.Router, cfg *config.ClientAPI,
	eduAPI eduServerAPI.EDUServerInputAPI,
	rsAPI roomserverAPI.RoomserverInternalAPI,
	asAPI appserviceAPI.AppServiceQueryAPI,
	accountDB accounts.Database,
	userAPI userapi.UserInternalAPI,
	federation *gomatrixserverlib.FederationClient,
	syncProducer *producers.SyncAPIProducer,
	transactionsCache *transactions.Cache,
	federationSender federationAPI.FederationInternalAPI,
	keyAPI keyserverAPI.KeyInternalAPI,
	extRoomsProvider api.ExtraPublicRoomsProvider,
	mscCfg *config.MSCs,
) {
	rateLimits := httputil.NewRateLimits(&cfg.RateLimiting)
	userInteractiveAuth := auth.NewUserInteractive(accountDB, cfg)

	unstableFeatures := map[string]bool{
		"org.matrix.e2e_cross_signing": true,
	}
	for _, msc := range cfg.MSCs.MSCs {
		unstableFeatures["org.matrix."+msc] = true
	}

	publicAPIMux.Handle("/versions",
		httputil.MakeExternalAPI("versions", func(req *http.Request) util.JSONResponse {
			return util.JSONResponse{
				Code: http.StatusOK,
				JSON: struct {
					Versions         []string        `json:"versions"`
					UnstableFeatures map[string]bool `json:"unstable_features"`
				}{Versions: []string{
					"r0.0.1",
					"r0.1.0",
					"r0.2.0",
					"r0.3.0",
					"r0.4.0",
					"r0.5.0",
					"r0.6.1",
				}, UnstableFeatures: unstableFeatures},
			}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	if cfg.RegistrationSharedSecret != "" {
		logrus.Info("Enabling shared secret registration at /_synapse/admin/v1/register")
		sr := NewSharedSecretRegistration(cfg.RegistrationSharedSecret)
		synapseAdminRouter.Handle("/admin/v1/register",
			httputil.MakeExternalAPI("shared_secret_registration", func(req *http.Request) util.JSONResponse {
				if req.Method == http.MethodGet {
					return util.JSONResponse{
						Code: 200,
						JSON: struct {
							Nonce string `json:"nonce"`
						}{
							Nonce: sr.GenerateNonce(),
						},
					}
				}
				if req.Method == http.MethodPost {
					return handleSharedSecretRegistration(userAPI, sr, req)
				}
				return util.JSONResponse{
					Code: http.StatusMethodNotAllowed,
					JSON: jsonerror.NotFound("unknown method"),
				}
			}),
		).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)
	}

	r0mux := publicAPIMux.PathPrefix("/r0").Subrouter()
	unstableMux := publicAPIMux.PathPrefix("/unstable").Subrouter()

	r0mux.Handle("/createRoom",
		httputil.MakeAuthAPI("createRoom", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return CreateRoom(req, device, cfg, accountDB, rsAPI, asAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/join/{roomIDOrAlias}",
		httputil.MakeAuthAPI(gomatrixserverlib.Join, userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return JoinRoomByIDOrAlias(
				req, device, rsAPI, accountDB, vars["roomIDOrAlias"],
			)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	if mscCfg.Enabled("msc2753") {
		r0mux.Handle("/peek/{roomIDOrAlias}",
			httputil.MakeAuthAPI(gomatrixserverlib.Peek, userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
				if r := rateLimits.Limit(req); r != nil {
					return *r
				}
				vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
				if err != nil {
					return util.ErrorResponse(err)
				}
				return PeekRoomByIDOrAlias(
					req, device, rsAPI, accountDB, vars["roomIDOrAlias"],
				)
			}),
		).Methods(http.MethodPost, http.MethodOptions)
	}
	r0mux.Handle("/joined_rooms",
		httputil.MakeAuthAPI("joined_rooms", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return GetJoinedRooms(req, device, rsAPI)
		}),
	).Methods(http.MethodGet, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/join",
		httputil.MakeAuthAPI(gomatrixserverlib.Join, userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return JoinRoomByIDOrAlias(
				req, device, rsAPI, accountDB, vars["roomID"],
			)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/leave",
		httputil.MakeAuthAPI("membership", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return LeaveRoomByID(
				req, device, rsAPI, vars["roomID"],
			)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/unpeek",
		httputil.MakeAuthAPI("unpeek", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return UnpeekRoomByID(
				req, device, rsAPI, accountDB, vars["roomID"],
			)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/ban",
		httputil.MakeAuthAPI("membership", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendBan(req, accountDB, device, vars["roomID"], cfg, rsAPI, asAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/invite",
		httputil.MakeAuthAPI("membership", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendInvite(req, accountDB, device, vars["roomID"], cfg, rsAPI, asAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/kick",
		httputil.MakeAuthAPI("membership", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendKick(req, accountDB, device, vars["roomID"], cfg, rsAPI, asAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/unban",
		httputil.MakeAuthAPI("membership", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendUnban(req, accountDB, device, vars["roomID"], cfg, rsAPI, asAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/send/{eventType}",
		httputil.MakeAuthAPI("send_message", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendEvent(req, device, vars["roomID"], vars["eventType"], nil, nil, cfg, rsAPI, nil)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/send/{eventType}/{txnID}",
		httputil.MakeAuthAPI("send_message", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			txnID := vars["txnID"]
			return SendEvent(req, device, vars["roomID"], vars["eventType"], &txnID,
				nil, cfg, rsAPI, transactionsCache)
		}),
	).Methods(http.MethodPut, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/event/{eventID}",
		httputil.MakeAuthAPI("rooms_get_event", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetEvent(req, device, vars["roomID"], vars["eventID"], cfg, rsAPI, federation)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/state", httputil.MakeAuthAPI("room_state", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return OnIncomingStateRequest(req.Context(), device, rsAPI, vars["roomID"])
	})).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/aliases", httputil.MakeAuthAPI("aliases", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return GetAliases(req, rsAPI, device, vars["roomID"])
	})).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/state/{type:[^/]+/?}", httputil.MakeAuthAPI("room_state", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		// If there's a trailing slash, remove it
		eventType := strings.TrimSuffix(vars["type"], "/")
		eventFormat := req.URL.Query().Get("format") == "event"
		return OnIncomingStateTypeRequest(req.Context(), device, rsAPI, vars["roomID"], eventType, "", eventFormat)
	})).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/state/{type}/{stateKey}", httputil.MakeAuthAPI("room_state", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		eventFormat := req.URL.Query().Get("format") == "event"
		return OnIncomingStateTypeRequest(req.Context(), device, rsAPI, vars["roomID"], vars["type"], vars["stateKey"], eventFormat)
	})).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/state/{eventType:[^/]+/?}",
		httputil.MakeAuthAPI("send_message", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			emptyString := ""
			eventType := strings.TrimSuffix(vars["eventType"], "/")
			return SendEvent(req, device, vars["roomID"], eventType, nil, &emptyString, cfg, rsAPI, nil)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/state/{eventType}/{stateKey}",
		httputil.MakeAuthAPI("send_message", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			stateKey := vars["stateKey"]
			return SendEvent(req, device, vars["roomID"], vars["eventType"], nil, &stateKey, cfg, rsAPI, nil)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/register", httputil.MakeExternalAPI("register", func(req *http.Request) util.JSONResponse {
		if r := rateLimits.Limit(req); r != nil {
			return *r
		}
		return Register(req, userAPI, accountDB, cfg)
	})).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/register/available", httputil.MakeExternalAPI("registerAvailable", func(req *http.Request) util.JSONResponse {
		if r := rateLimits.Limit(req); r != nil {
			return *r
		}
		return RegisterAvailable(req, cfg, accountDB)
	})).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/directory/room/{roomAlias}",
		httputil.MakeExternalAPI("directory_room", func(req *http.Request) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return DirectoryRoom(req, vars["roomAlias"], federation, cfg, rsAPI, federationSender)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/directory/room/{roomAlias}",
		httputil.MakeAuthAPI("directory_room", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SetLocalAlias(req, device, vars["roomAlias"], cfg, rsAPI)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/directory/room/{roomAlias}",
		httputil.MakeAuthAPI("directory_room", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return RemoveLocalAlias(req, device, vars["roomAlias"], rsAPI)
		}),
	).Methods(http.MethodDelete, http.MethodOptions)
	r0mux.Handle("/directory/list/room/{roomID}",
		httputil.MakeExternalAPI("directory_list", func(req *http.Request) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetVisibility(req, rsAPI, vars["roomID"])
		}),
	).Methods(http.MethodGet, http.MethodOptions)
	// TODO: Add AS support
	r0mux.Handle("/directory/list/room/{roomID}",
		httputil.MakeAuthAPI("directory_list", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SetVisibility(req, rsAPI, device, vars["roomID"])
		}),
	).Methods(http.MethodPut, http.MethodOptions)
	r0mux.Handle("/publicRooms",
		httputil.MakeExternalAPI("public_rooms", func(req *http.Request) util.JSONResponse {
			return GetPostPublicRooms(req, rsAPI, extRoomsProvider, federation, cfg)
		}),
	).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)

	r0mux.Handle("/logout",
		httputil.MakeAuthAPI("logout", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return Logout(req, userAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/logout/all",
		httputil.MakeAuthAPI("logout", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return LogoutAll(req, userAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/typing/{userID}",
		httputil.MakeAuthAPI("rooms_typing", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendTyping(req, device, vars["roomID"], vars["userID"], accountDB, eduAPI, rsAPI)
		}),
	).Methods(http.MethodPut, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/redact/{eventID}",
		httputil.MakeAuthAPI("rooms_redact", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendRedaction(req, device, vars["roomID"], vars["eventID"], cfg, rsAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomID}/redact/{eventID}/{txnId}",
		httputil.MakeAuthAPI("rooms_redact", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendRedaction(req, device, vars["roomID"], vars["eventID"], cfg, rsAPI)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/sendToDevice/{eventType}/{txnID}",
		httputil.MakeAuthAPI("send_to_device", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			txnID := vars["txnID"]
			return SendToDevice(req, device, eduAPI, transactionsCache, vars["eventType"], &txnID)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	// This is only here because sytest refers to /unstable for this endpoint
	// rather than r0. It's an exact duplicate of the above handler.
	// TODO: Remove this if/when sytest is fixed!
	unstableMux.Handle("/sendToDevice/{eventType}/{txnID}",
		httputil.MakeAuthAPI("send_to_device", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			txnID := vars["txnID"]
			return SendToDevice(req, device, eduAPI, transactionsCache, vars["eventType"], &txnID)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/account/whoami",
		httputil.MakeAuthAPI("whoami", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return Whoami(req, device)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/account/password",
		httputil.MakeAuthAPI("password", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return Password(req, userAPI, accountDB, device, cfg)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/account/deactivate",
		httputil.MakeAuthAPI("deactivate", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return Deactivate(req, userInteractiveAuth, userAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	// Stub endpoints required by Element

	r0mux.Handle("/login",
		httputil.MakeExternalAPI("login", func(req *http.Request) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return Login(req, accountDB, userAPI, cfg)
		}),
	).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)

	r0mux.Handle("/auth/{authType}/fallback/web",
		httputil.MakeHTMLAPI("auth_fallback", func(w http.ResponseWriter, req *http.Request) *util.JSONResponse {
			vars := mux.Vars(req)
			return AuthFallback(w, req, vars["authType"], cfg)
		}),
	).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)

	r0mux.Handle("/pushrules/",
		httputil.MakeExternalAPI("push_rules", func(req *http.Request) util.JSONResponse {
			// TODO: Implement push rules API
			res := json.RawMessage(`{
					"global": {
						"content": [],
						"override": [],
						"room": [],
						"sender": [],
						"underride": []
					}
				}`)
			return util.JSONResponse{
				Code: http.StatusOK,
				JSON: &res,
			}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	// Element user settings

	r0mux.Handle("/profile/{userID}",
		httputil.MakeExternalAPI("profile", func(req *http.Request) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetProfile(req, accountDB, cfg, vars["userID"], asAPI, federation)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/profile/{userID}/avatar_url",
		httputil.MakeExternalAPI("profile_avatar_url", func(req *http.Request) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetAvatarURL(req, accountDB, cfg, vars["userID"], asAPI, federation)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/profile/{userID}/avatar_url",
		httputil.MakeAuthAPI("profile_avatar_url", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SetAvatarURL(req, accountDB, device, vars["userID"], cfg, rsAPI)
		}),
	).Methods(http.MethodPut, http.MethodOptions)
	// Browsers use the OPTIONS HTTP method to check if the CORS policy allows
	// PUT requests, so we need to allow this method

	r0mux.Handle("/profile/{userID}/displayname",
		httputil.MakeExternalAPI("profile_displayname", func(req *http.Request) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetDisplayName(req, accountDB, cfg, vars["userID"], asAPI, federation)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/profile/{userID}/displayname",
		httputil.MakeAuthAPI("profile_displayname", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SetDisplayName(req, accountDB, device, vars["userID"], cfg, rsAPI)
		}),
	).Methods(http.MethodPut, http.MethodOptions)
	// Browsers use the OPTIONS HTTP method to check if the CORS policy allows
	// PUT requests, so we need to allow this method

	r0mux.Handle("/account/3pid",
		httputil.MakeAuthAPI("account_3pid", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return GetAssociated3PIDs(req, accountDB, device)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/account/3pid",
		httputil.MakeAuthAPI("account_3pid", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return CheckAndSave3PIDAssociation(req, accountDB, device, cfg)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	unstableMux.Handle("/account/3pid/delete",
		httputil.MakeAuthAPI("account_3pid", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return Forget3PID(req, accountDB)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/{path:(?:account/3pid|register)}/email/requestToken",
		httputil.MakeExternalAPI("account_3pid_request_token", func(req *http.Request) util.JSONResponse {
			return RequestEmailToken(req, accountDB, cfg)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	// Element logs get flooded unless this is handled
	r0mux.Handle("/presence/{userID}/status",
		httputil.MakeExternalAPI("presence", func(req *http.Request) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			// TODO: Set presence (probably the responsibility of a presence server not clientapi)
			return util.JSONResponse{
				Code: http.StatusOK,
				JSON: struct{}{},
			}
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/voip/turnServer",
		httputil.MakeAuthAPI("turn_server", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return RequestTurnServer(req, device, cfg)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/thirdparty/protocols",
		httputil.MakeExternalAPI("thirdparty_protocols", func(req *http.Request) util.JSONResponse {
			// TODO: Return the third party protcols
			return util.JSONResponse{
				Code: http.StatusOK,
				JSON: struct{}{},
			}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/initialSync",
		httputil.MakeExternalAPI("rooms_initial_sync", func(req *http.Request) util.JSONResponse {
			// TODO: Allow people to peek into rooms.
			return util.JSONResponse{
				Code: http.StatusForbidden,
				JSON: jsonerror.GuestAccessForbidden("Guest access not implemented"),
			}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/user/{userID}/account_data/{type}",
		httputil.MakeAuthAPI("user_account_data", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SaveAccountData(req, userAPI, device, vars["userID"], "", vars["type"], syncProducer)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/user/{userID}/rooms/{roomID}/account_data/{type}",
		httputil.MakeAuthAPI("user_account_data", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SaveAccountData(req, userAPI, device, vars["userID"], vars["roomID"], vars["type"], syncProducer)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/user/{userID}/account_data/{type}",
		httputil.MakeAuthAPI("user_account_data", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetAccountData(req, userAPI, device, vars["userID"], "", vars["type"])
		}),
	).Methods(http.MethodGet)

	r0mux.Handle("/user/{userID}/rooms/{roomID}/account_data/{type}",
		httputil.MakeAuthAPI("user_account_data", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetAccountData(req, userAPI, device, vars["userID"], vars["roomID"], vars["type"])
		}),
	).Methods(http.MethodGet)

	r0mux.Handle("/admin/whois/{userID}",
		httputil.MakeAuthAPI("admin_whois", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetAdminWhois(req, userAPI, device, vars["userID"])
		}),
	).Methods(http.MethodGet)

	r0mux.Handle("/user/{userID}/openid/request_token",
		httputil.MakeAuthAPI("openid_request_token", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return CreateOpenIDToken(req, userAPI, device, vars["userID"], cfg)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/user_directory/search",
		httputil.MakeAuthAPI("userdirectory_search", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			postContent := struct {
				SearchString string `json:"search_term"`
				Limit        int    `json:"limit"`
			}{}

			if resErr := clientutil.UnmarshalJSONRequest(req, &postContent); resErr != nil {
				return *resErr
			}
			return *SearchUserDirectory(
				req.Context(),
				device,
				userAPI,
				rsAPI,
				cfg.Matrix.ServerName,
				postContent.SearchString,
				postContent.Limit,
			)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/members",
		httputil.MakeAuthAPI("rooms_members", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetMemberships(req, device, vars["roomID"], false, cfg, rsAPI)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/joined_members",
		httputil.MakeAuthAPI("rooms_members", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetMemberships(req, device, vars["roomID"], true, cfg, rsAPI)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/read_markers",
		httputil.MakeAuthAPI("rooms_read_markers", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SaveReadMarker(req, userAPI, rsAPI, eduAPI, syncProducer, device, vars["roomID"])
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/rooms/{roomID}/forget",
		httputil.MakeAuthAPI("rooms_forget", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return SendForget(req, device, vars["roomID"], rsAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	r0mux.Handle("/devices",
		httputil.MakeAuthAPI("get_devices", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return GetDevicesByLocalpart(req, userAPI, device)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/devices/{deviceID}",
		httputil.MakeAuthAPI("get_device", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetDeviceByID(req, userAPI, device, vars["deviceID"])
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/devices/{deviceID}",
		httputil.MakeAuthAPI("device_data", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return UpdateDeviceByID(req, userAPI, device, vars["deviceID"])
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/devices/{deviceID}",
		httputil.MakeAuthAPI("delete_device", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return DeleteDeviceById(req, userInteractiveAuth, userAPI, device, vars["deviceID"])
		}),
	).Methods(http.MethodDelete, http.MethodOptions)

	r0mux.Handle("/delete_devices",
		httputil.MakeAuthAPI("delete_devices", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return DeleteDevices(req, userAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)

	// Stub implementations for sytest
	r0mux.Handle("/events",
		httputil.MakeExternalAPI("events", func(req *http.Request) util.JSONResponse {
			return util.JSONResponse{Code: http.StatusOK, JSON: map[string]interface{}{
				"chunk": []interface{}{},
				"start": "",
				"end":   "",
			}}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/initialSync",
		httputil.MakeExternalAPI("initial_sync", func(req *http.Request) util.JSONResponse {
			return util.JSONResponse{Code: http.StatusOK, JSON: map[string]interface{}{
				"end": "",
			}}
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/user/{userId}/rooms/{roomId}/tags",
		httputil.MakeAuthAPI("get_tags", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return GetTags(req, userAPI, device, vars["userId"], vars["roomId"], syncProducer)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	r0mux.Handle("/user/{userId}/rooms/{roomId}/tags/{tag}",
		httputil.MakeAuthAPI("put_tag", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return PutTag(req, userAPI, device, vars["userId"], vars["roomId"], vars["tag"], syncProducer)
		}),
	).Methods(http.MethodPut, http.MethodOptions)

	r0mux.Handle("/user/{userId}/rooms/{roomId}/tags/{tag}",
		httputil.MakeAuthAPI("delete_tag", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}
			return DeleteTag(req, userAPI, device, vars["userId"], vars["roomId"], vars["tag"], syncProducer)
		}),
	).Methods(http.MethodDelete, http.MethodOptions)

	r0mux.Handle("/capabilities",
		httputil.MakeAuthAPI("capabilities", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			return GetCapabilities(req, rsAPI)
		}),
	).Methods(http.MethodGet, http.MethodOptions)

	// Key Backup Versions (Metadata)

	getBackupKeysVersion := httputil.MakeAuthAPI("get_backup_keys_version", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return KeyBackupVersion(req, userAPI, device, vars["version"])
	})

	getLatestBackupKeysVersion := httputil.MakeAuthAPI("get_latest_backup_keys_version", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		return KeyBackupVersion(req, userAPI, device, "")
	})

	putBackupKeysVersion := httputil.MakeAuthAPI("put_backup_keys_version", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return ModifyKeyBackupVersionAuthData(req, userAPI, device, vars["version"])
	})

	deleteBackupKeysVersion := httputil.MakeAuthAPI("delete_backup_keys_version", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return DeleteKeyBackupVersion(req, userAPI, device, vars["version"])
	})

	postNewBackupKeysVersion := httputil.MakeAuthAPI("post_new_backup_keys_version", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		return CreateKeyBackupVersion(req, userAPI, device)
	})

	r0mux.Handle("/room_keys/version/{version}", getBackupKeysVersion).Methods(http.MethodGet, http.MethodOptions)
	r0mux.Handle("/room_keys/version", getLatestBackupKeysVersion).Methods(http.MethodGet, http.MethodOptions)
	r0mux.Handle("/room_keys/version/{version}", putBackupKeysVersion).Methods(http.MethodPut)
	r0mux.Handle("/room_keys/version/{version}", deleteBackupKeysVersion).Methods(http.MethodDelete)
	r0mux.Handle("/room_keys/version", postNewBackupKeysVersion).Methods(http.MethodPost, http.MethodOptions)

	unstableMux.Handle("/room_keys/version/{version}", getBackupKeysVersion).Methods(http.MethodGet, http.MethodOptions)
	unstableMux.Handle("/room_keys/version", getLatestBackupKeysVersion).Methods(http.MethodGet, http.MethodOptions)
	unstableMux.Handle("/room_keys/version/{version}", putBackupKeysVersion).Methods(http.MethodPut)
	unstableMux.Handle("/room_keys/version/{version}", deleteBackupKeysVersion).Methods(http.MethodDelete)
	unstableMux.Handle("/room_keys/version", postNewBackupKeysVersion).Methods(http.MethodPost, http.MethodOptions)

	// Inserting E2E Backup Keys

	// Bulk room and session
	putBackupKeys := httputil.MakeAuthAPI("put_backup_keys", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		version := req.URL.Query().Get("version")
		if version == "" {
			return util.JSONResponse{
				Code: 400,
				JSON: jsonerror.InvalidArgumentValue("version must be specified"),
			}
		}
		var reqBody keyBackupSessionRequest
		resErr := clientutil.UnmarshalJSONRequest(req, &reqBody)
		if resErr != nil {
			return *resErr
		}
		return UploadBackupKeys(req, userAPI, device, version, &reqBody)
	})

	// Single room bulk session
	putBackupKeysRoom := httputil.MakeAuthAPI("put_backup_keys_room", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		version := req.URL.Query().Get("version")
		if version == "" {
			return util.JSONResponse{
				Code: 400,
				JSON: jsonerror.InvalidArgumentValue("version must be specified"),
			}
		}
		roomID := vars["roomID"]
		var reqBody keyBackupSessionRequest
		reqBody.Rooms = make(map[string]struct {
			Sessions map[string]userapi.KeyBackupSession `json:"sessions"`
		})
		reqBody.Rooms[roomID] = struct {
			Sessions map[string]userapi.KeyBackupSession `json:"sessions"`
		}{
			Sessions: map[string]userapi.KeyBackupSession{},
		}
		body := reqBody.Rooms[roomID]
		resErr := clientutil.UnmarshalJSONRequest(req, &body)
		if resErr != nil {
			return *resErr
		}
		reqBody.Rooms[roomID] = body
		return UploadBackupKeys(req, userAPI, device, version, &reqBody)
	})

	// Single room, single session
	putBackupKeysRoomSession := httputil.MakeAuthAPI("put_backup_keys_room_session", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		version := req.URL.Query().Get("version")
		if version == "" {
			return util.JSONResponse{
				Code: 400,
				JSON: jsonerror.InvalidArgumentValue("version must be specified"),
			}
		}
		var reqBody userapi.KeyBackupSession
		resErr := clientutil.UnmarshalJSONRequest(req, &reqBody)
		if resErr != nil {
			return *resErr
		}
		roomID := vars["roomID"]
		sessionID := vars["sessionID"]
		var keyReq keyBackupSessionRequest
		keyReq.Rooms = make(map[string]struct {
			Sessions map[string]userapi.KeyBackupSession `json:"sessions"`
		})
		keyReq.Rooms[roomID] = struct {
			Sessions map[string]userapi.KeyBackupSession `json:"sessions"`
		}{
			Sessions: make(map[string]userapi.KeyBackupSession),
		}
		keyReq.Rooms[roomID].Sessions[sessionID] = reqBody
		return UploadBackupKeys(req, userAPI, device, version, &keyReq)
	})

	r0mux.Handle("/room_keys/keys", putBackupKeys).Methods(http.MethodPut)
	r0mux.Handle("/room_keys/keys/{roomID}", putBackupKeysRoom).Methods(http.MethodPut)
	r0mux.Handle("/room_keys/keys/{roomID}/{sessionID}", putBackupKeysRoomSession).Methods(http.MethodPut)

	unstableMux.Handle("/room_keys/keys", putBackupKeys).Methods(http.MethodPut)
	unstableMux.Handle("/room_keys/keys/{roomID}", putBackupKeysRoom).Methods(http.MethodPut)
	unstableMux.Handle("/room_keys/keys/{roomID}/{sessionID}", putBackupKeysRoomSession).Methods(http.MethodPut)

	// Querying E2E Backup Keys

	getBackupKeys := httputil.MakeAuthAPI("get_backup_keys", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		return GetBackupKeys(req, userAPI, device, req.URL.Query().Get("version"), "", "")
	})

	getBackupKeysRoom := httputil.MakeAuthAPI("get_backup_keys_room", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return GetBackupKeys(req, userAPI, device, req.URL.Query().Get("version"), vars["roomID"], "")
	})

	getBackupKeysRoomSession := httputil.MakeAuthAPI("get_backup_keys_room_session", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
		if err != nil {
			return util.ErrorResponse(err)
		}
		return GetBackupKeys(req, userAPI, device, req.URL.Query().Get("version"), vars["roomID"], vars["sessionID"])
	})

	r0mux.Handle("/room_keys/keys", getBackupKeys).Methods(http.MethodGet, http.MethodOptions)
	r0mux.Handle("/room_keys/keys/{roomID}", getBackupKeysRoom).Methods(http.MethodGet, http.MethodOptions)
	r0mux.Handle("/room_keys/keys/{roomID}/{sessionID}", getBackupKeysRoomSession).Methods(http.MethodGet, http.MethodOptions)

	unstableMux.Handle("/room_keys/keys", getBackupKeys).Methods(http.MethodGet, http.MethodOptions)
	unstableMux.Handle("/room_keys/keys/{roomID}", getBackupKeysRoom).Methods(http.MethodGet, http.MethodOptions)
	unstableMux.Handle("/room_keys/keys/{roomID}/{sessionID}", getBackupKeysRoomSession).Methods(http.MethodGet, http.MethodOptions)

	// Deleting E2E Backup Keys

	// Cross-signing device keys

	postDeviceSigningKeys := httputil.MakeAuthAPI("post_device_signing_keys", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		return UploadCrossSigningDeviceKeys(req, userInteractiveAuth, keyAPI, device, accountDB, cfg)
	})

	postDeviceSigningSignatures := httputil.MakeAuthAPI("post_device_signing_signatures", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
		return UploadCrossSigningDeviceSignatures(req, keyAPI, device)
	})

	r0mux.Handle("/keys/device_signing/upload", postDeviceSigningKeys).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/keys/signatures/upload", postDeviceSigningSignatures).Methods(http.MethodPost, http.MethodOptions)

	unstableMux.Handle("/keys/device_signing/upload", postDeviceSigningKeys).Methods(http.MethodPost, http.MethodOptions)
	unstableMux.Handle("/keys/signatures/upload", postDeviceSigningSignatures).Methods(http.MethodPost, http.MethodOptions)

	// Supplying a device ID is deprecated.
	r0mux.Handle("/keys/upload/{deviceID}",
		httputil.MakeAuthAPI("keys_upload", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return UploadKeys(req, keyAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/keys/upload",
		httputil.MakeAuthAPI("keys_upload", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return UploadKeys(req, keyAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/keys/query",
		httputil.MakeAuthAPI("keys_query", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return QueryKeys(req, keyAPI, device)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/keys/claim",
		httputil.MakeAuthAPI("keys_claim", userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			return ClaimKeys(req, keyAPI)
		}),
	).Methods(http.MethodPost, http.MethodOptions)
	r0mux.Handle("/rooms/{roomId}/receipt/{receiptType}/{eventId}",
		httputil.MakeAuthAPI(gomatrixserverlib.Join, userAPI, func(req *http.Request, device *userapi.Device) util.JSONResponse {
			if r := rateLimits.Limit(req); r != nil {
				return *r
			}
			vars, err := httputil.URLDecodeMapValues(mux.Vars(req))
			if err != nil {
				return util.ErrorResponse(err)
			}

			return SetReceipt(req, eduAPI, device, vars["roomId"], vars["receiptType"], vars["eventId"])
		}),
	).Methods(http.MethodPost, http.MethodOptions)
}
