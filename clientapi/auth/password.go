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

package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/matrix-org/dendrite/clientapi/auth/authtypes"
	"github.com/matrix-org/dendrite/clientapi/httputil"
	"github.com/matrix-org/dendrite/clientapi/jsonerror"
	"github.com/matrix-org/dendrite/clientapi/userutil"
	"github.com/matrix-org/dendrite/setup/config"
	"github.com/matrix-org/dendrite/userapi/api"
	"github.com/matrix-org/util"
)

type GetAccountByPassword func(ctx context.Context, localpart, password string) (*api.Account, error)

type PasswordRequest struct {
	Login
	Password string `json:"password"`
}

// LoginTypePassword implements https://matrix.org/docs/spec/client_server/r0.6.1#password-based
type LoginTypePassword struct {
	GetAccountByPassword GetAccountByPassword
	Config               *config.ClientAPI
}

func (t *LoginTypePassword) Name() string {
	return authtypes.LoginTypePassword
}

func (t *LoginTypePassword) LoginFromJSON(ctx context.Context, reqBytes []byte) (*Login, LoginCleanupFunc, *util.JSONResponse) {
	var r PasswordRequest
	if err := httputil.UnmarshalJSON(reqBytes, &r); err != nil {
		return nil, nil, err
	}

	login, err := t.Login(ctx, &r)
	if err != nil {
		return nil, nil, err
	}

	return login, func(context.Context, *util.JSONResponse) {}, nil
}

func (t *LoginTypePassword) Login(ctx context.Context, req interface{}) (*Login, *util.JSONResponse) {
	r := req.(*PasswordRequest)
  username := strings.ToLower(r.Username())
	if username == "" {
		return nil, &util.JSONResponse{
			Code: http.StatusUnauthorized,
			JSON: jsonerror.BadJSON("A username must be supplied."),
		}
	}
	localpart, err := userutil.ParseUsernameParam(username, &t.Config.Matrix.ServerName)
	if err != nil {
		return nil, &util.JSONResponse{
			Code: http.StatusUnauthorized,
			JSON: jsonerror.InvalidUsername(err.Error()),
		}
	}
	// Squash username to all lowercase letters
	_, err = t.GetAccountByPassword(ctx, strings.ToLower(localpart), r.Password)
	if err != nil {
		if err == sql.ErrNoRows {
			_, err = t.GetAccountByPassword(ctx, localpart, r.Password)
			if err == nil {
				return &r.Login, nil
			}
		}
		// Technically we could tell them if the user does not exist by checking if err == sql.ErrNoRows
		// but that would leak the existence of the user.
		return nil, &util.JSONResponse{
			Code: http.StatusForbidden,
			JSON: jsonerror.Forbidden("The username or password was incorrect or the account does not exist."),
		}
	}
	return &r.Login, nil
}
