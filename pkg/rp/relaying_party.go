package rp

import (
	"context"
	"net/http"
	"strings"

	"github.com/caos/oidc/pkg/oidc"
	"github.com/caos/oidc/pkg/oidc/grants"
	"github.com/caos/oidc/pkg/utils"

	"golang.org/x/oauth2"
)

//RelayingParty declares the minimal interface for oidc clients
type RelayingParty interface {
	//OAuthConfig returns the oauth2 Config
	OAuthConfig() *oauth2.Config

	//IsPKCE returns if authorization is done using `Authorization Code Flow with Proof Key for Code Exchange (PKCE)`
	IsPKCE() bool

	//CookieHandler returns a http cookie handler used for various state transfer cookies
	CookieHandler() *utils.CookieHandler

	//Client return a standard http client where the token can be used
	Client(ctx context.Context, token *oauth2.Token) *http.Client

	/*
		//AuthURL returns the authorization endpoint with a given state
		AuthURL(state string, opts ...AuthURLOpt) string

		//AuthURLHandler should implement the AuthURL func as http.HandlerFunc
		//(redirecting to the auth endpoint)
		AuthURLHandler(state string) http.HandlerFunc

		//CodeExchange implements the OIDC Token Request (oauth2 Authorization Code Grant)
		//returning an `Access Token` and `ID Token Claims`
		CodeExchange(ctx context.Context, code string, opts ...CodeExchangeOpt) (*oidc.Tokens, error)

		//CodeExchangeHandler extends the CodeExchange func,
		//calling the provided callback func on success with additional returned `state`
		CodeExchangeHandler(callback func(http.ResponseWriter, *http.Request, *oidc.Tokens, string)) http.HandlerFunc

		//ClientCredentials implements the oauth2 Client Credentials Grant
		//requesting an `Access Token` for the client itself, without user context
		ClientCredentials(ctx context.Context, scopes ...string) (*oauth2.Token, error)

		//Introspects calls the Introspect Endpoint
		//for validating an (access) token
		// Introspect(ctx context.Context, token string) (TokenIntrospectResponse, error)

		//Userinfo implements the OIDC Userinfo call
		//returning the info of the user for the requested scopes of an access token
		Userinfo()
	*/
	HttpClient() *http.Client
	IsOAuth2Only() bool
	IDTokenVerifier() IDTokenVerifier
	ErrorHandler() func(http.ResponseWriter, *http.Request, string, string, string)
}

//
////PasswortGrantRP extends the `RelayingParty` interface with the oauth2 `Password Grant`
////
////This interface is separated from the standard `RelayingParty` interface as the `password grant`
////is part of the oauth2 and therefore OIDC specification, but should only be used when there's no
////other possibility, so IMHO never ever. Ever.
//type PasswortGrantRP interface {
//	RelayingParty
//
//	//PasswordGrant implements the oauth2 `Password Grant`,
//	//requesting an access token with the users `username` and `password`
//	PasswordGrant(context.Context, string, string) (*oauth2.Token, error)
//}

var (
	DefaultErrorHandler = func(w http.ResponseWriter, r *http.Request, errorType string, errorDesc string, state string) {
		http.Error(w, errorType+": "+errorDesc, http.StatusInternalServerError)
	}
)

type relayingParty struct {
	endpoints Endpoints

	oauthConfig *oauth2.Config
	config      *Configuration
	pkce        bool

	httpClient    *http.Client
	cookieHandler *utils.CookieHandler

	errorHandler func(http.ResponseWriter, *http.Request, string, string, string)

	idTokenVerifier IDTokenVerifier
	verifierOpts    []ConfFunc
	oauth2Only      bool
}

func (rp *relayingParty) OAuthConfig() *oauth2.Config {
	return rp.oauthConfig
}

func (rp *relayingParty) IsPKCE() bool {
	return rp.pkce
}

func (rp *relayingParty) CookieHandler() *utils.CookieHandler {
	return rp.cookieHandler
}

func (rp *relayingParty) HttpClient() *http.Client {
	return rp.httpClient
}

func (rp *relayingParty) IsOAuth2Only() bool {
	return rp.oauth2Only
}

func (rp *relayingParty) IDTokenVerifier() IDTokenVerifier {
	return rp.idTokenVerifier
}

func (rp *relayingParty) Client(ctx context.Context, token *oauth2.Token) *http.Client {
	panic("implement me")
}

func (rp *relayingParty) ErrorHandler() func(http.ResponseWriter, *http.Request, string, string, string) {
	return rp.errorHandler
}

//NewRelayingParty creates a DelegationTokenExchangeRP with the given
//Config and possible configOptions
//it will run discovery on the provided issuer if AuthURL and TokenURL are not set
//if no verifier is provided using the options the `DefaultVerifier` is set
func NewRelayingParty(config *Configuration, options ...Option) (RelayingParty, error) {
	isOpenID := isOpenID(config.Scopes)

	rp := &relayingParty{
		config:     config,
		httpClient: utils.DefaultHTTPClient,
		oauth2Only: !isOpenID,
	}

	for _, optFunc := range options {
		optFunc(rp)
	}

	rp.oauthConfig = config.Config
	if config.Endpoint.AuthURL != "" && config.Endpoint.TokenURL != "" {
		rp.oauthConfig = config.Config
	} else {
		endpoints, err := Discover(config.Issuer, rp.httpClient)
		if err != nil {
			return nil, err
		}
		rp.oauthConfig.Endpoint = endpoints.Endpoint
		rp.endpoints = endpoints
	}

	if rp.errorHandler == nil {
		rp.errorHandler = DefaultErrorHandler
	}

	if isOpenID && rp.idTokenVerifier == nil {
		rp.idTokenVerifier = NewIDTokenVerifier(config.Issuer, config.ClientID, NewRemoteKeySet(rp.httpClient, rp.endpoints.JKWsURL))
	}

	return rp, nil
}

//DefaultRPOpts is the type for providing dynamic options to the DefaultRP
type Option func(*relayingParty)

//WithCookieHandler set a `CookieHandler` for securing the various redirects
func WithCookieHandler(cookieHandler *utils.CookieHandler) Option {
	return func(rp *relayingParty) {
		rp.cookieHandler = cookieHandler
	}
}

//WithPKCE sets the RP to use PKCE (oauth2 code challenge)
//it also sets a `CookieHandler` for securing the various redirects
//and exchanging the code challenge
func WithPKCE(cookieHandler *utils.CookieHandler) Option {
	return func(rp *relayingParty) {
		rp.pkce = true
		rp.cookieHandler = cookieHandler
	}
}

//WithHTTPClient provides the ability to set an http client to be used for the relaying party and verifier
func WithHTTPClient(client *http.Client) Option {
	return func(rp *relayingParty) {
		rp.httpClient = client
	}
}

func WithVerifierOpts(opts ...ConfFunc) Option {
	return func(rp *relayingParty) {
		rp.verifierOpts = opts
	}
}

//Discover calls the discovery endpoint of the provided issuer and returns the found endpoints
func Discover(issuer string, httpClient *http.Client) (Endpoints, error) {
	wellKnown := strings.TrimSuffix(issuer, "/") + oidc.DiscoveryEndpoint
	req, err := http.NewRequest("GET", wellKnown, nil)
	if err != nil {
		return Endpoints{}, err
	}
	discoveryConfig := new(oidc.DiscoveryConfiguration)
	err = utils.HttpRequest(httpClient, req, &discoveryConfig)
	if err != nil {
		return Endpoints{}, err
	}
	return GetEndpoints(discoveryConfig), nil
}

//AuthURL returns the auth request url
//(wrapping the oauth2 `AuthCodeURL`)
func AuthURL(state string, rp RelayingParty, opts ...AuthURLOpt) string {
	authOpts := make([]oauth2.AuthCodeOption, 0)
	for _, opt := range opts {
		authOpts = append(authOpts, opt()...)
	}
	return rp.OAuthConfig().AuthCodeURL(state, authOpts...)
}

//AuthURLHandler extends the `AuthURL` method with a http redirect handler
//including handling setting cookie for secure `state` transfer
func AuthURLHandler(state string, rp RelayingParty) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := make([]AuthURLOpt, 0)
		if err := trySetStateCookie(w, state, rp); err != nil {
			http.Error(w, "failed to create state cookie: "+err.Error(), http.StatusUnauthorized)
			return
		}
		if rp.IsPKCE() {
			codeChallenge, err := GenerateAndStoreCodeChallenge(w, rp)
			if err != nil {
				http.Error(w, "failed to create code challenge: "+err.Error(), http.StatusUnauthorized)
				return
			}
			opts = append(opts, WithCodeChallenge(codeChallenge))
		}
		http.Redirect(w, r, AuthURL(state, rp, opts...), http.StatusFound)
	}
}

func GenerateAndStoreCodeChallenge(w http.ResponseWriter, rp RelayingParty) (string, error) {
	var codeVerifier string
	codeVerifier = "s"
	if err := rp.CookieHandler().SetCookie(w, pkceCode, codeVerifier); err != nil {
		return "", err
	}
	return oidc.NewSHACodeChallenge(codeVerifier), nil
}

//AuthURL is the `RelayingParty` interface implementation
//handling the oauth2 code exchange, extracting and validating the id_token
//returning it paresed together with the oauth2 tokens (access, refresh)
func CodeExchange(ctx context.Context, code string, rp RelayingParty, opts ...CodeExchangeOpt) (tokens *oidc.Tokens, err error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, rp.HttpClient())
	codeOpts := make([]oauth2.AuthCodeOption, 0)
	for _, opt := range opts {
		codeOpts = append(codeOpts, opt()...)
	}

	token, err := rp.OAuthConfig().Exchange(ctx, code, codeOpts...)
	if err != nil {
		return nil, err //TODO: our error
	}
	idTokenString, ok := token.Extra(idTokenKey).(string)
	if !ok {
		//TODO: implement
	}

	idToken := new(oidc.IDTokenClaims)
	if !rp.IsOAuth2Only() {
		idToken, err = VerifyTokens(ctx, token.AccessToken, idTokenString, rp.IDTokenVerifier())
		if err != nil {
			return nil, err //TODO: err
		}
	}

	return &oidc.Tokens{Token: token, IDTokenClaims: idToken, IDToken: idTokenString}, nil
}

//AuthURL is the `RelayingParty` interface implementation
//extending the `CodeExchange` method with callback function
func CodeExchangeHandler(callback func(http.ResponseWriter, *http.Request, *oidc.Tokens, string), rp RelayingParty) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := tryReadStateCookie(w, r, rp)
		if err != nil {
			http.Error(w, "failed to get state: "+err.Error(), http.StatusUnauthorized)
			return
		}
		params := r.URL.Query()
		if params.Get("error") != "" {
			rp.ErrorHandler()(w, r, params.Get("error"), params.Get("error_description"), state)
			return
		}
		codeOpts := make([]CodeExchangeOpt, 0)
		if rp.IsPKCE() {
			codeVerifier, err := rp.CookieHandler().CheckCookie(r, pkceCode)
			if err != nil {
				http.Error(w, "failed to get code verifier: "+err.Error(), http.StatusUnauthorized)
				return
			}
			codeOpts = append(codeOpts, WithCodeVerifier(codeVerifier))
		}
		tokens, err := CodeExchange(r.Context(), params.Get("code"), rp, codeOpts...)
		if err != nil {
			http.Error(w, "failed to exchange token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		callback(w, r, tokens, state)
	}
}

//ClientCredentials is the `RelayingParty` interface implementation
//handling the oauth2 client credentials grant
func ClientCredentials(ctx context.Context, rp RelayingParty, scopes ...string) (newToken *oauth2.Token, err error) {
	return CallTokenEndpoint(grants.ClientCredentialsGrantBasic(scopes...), rp)
}

func CallTokenEndpoint(request interface{}, rp RelayingParty) (newToken *oauth2.Token, err error) {
	config := rp.OAuthConfig()
	req, err := utils.FormRequest(rp.OAuthConfig().Endpoint.TokenURL, request, config.ClientID, config.ClientSecret, config.Endpoint.AuthStyle != oauth2.AuthStyleInParams)
	if err != nil {
		return nil, err
	}
	token := new(oauth2.Token)
	if err := utils.HttpRequest(rp.HttpClient(), req, token); err != nil {
		return nil, err
	}
	return token, nil
}

func trySetStateCookie(w http.ResponseWriter, state string, rp RelayingParty) error {
	if rp.CookieHandler() != nil {
		if err := rp.CookieHandler().SetCookie(w, stateParam, state); err != nil {
			return err
		}
	}
	return nil
}

func tryReadStateCookie(w http.ResponseWriter, r *http.Request, rp RelayingParty) (state string, err error) {
	if rp.CookieHandler() == nil {
		return r.FormValue(stateParam), nil
	}
	state, err = rp.CookieHandler().CheckQueryCookie(r, stateParam)
	if err != nil {
		return "", err
	}
	rp.CookieHandler().DeleteCookie(w, stateParam)
	return state, nil
}

type Configuration struct {
	Issuer string
	*oauth2.Config
}

type OptionFunc func(RelayingParty)

type Endpoints struct {
	oauth2.Endpoint
	IntrospectURL string
	UserinfoURL   string
	JKWsURL       string
}

func GetEndpoints(discoveryConfig *oidc.DiscoveryConfiguration) Endpoints {
	return Endpoints{
		Endpoint: oauth2.Endpoint{
			AuthURL:   discoveryConfig.AuthorizationEndpoint,
			AuthStyle: oauth2.AuthStyleAutoDetect,
			TokenURL:  discoveryConfig.TokenEndpoint,
		},
		IntrospectURL: discoveryConfig.IntrospectionEndpoint,
		UserinfoURL:   discoveryConfig.UserinfoEndpoint,
		JKWsURL:       discoveryConfig.JwksURI,
	}
}

type AuthURLOpt func() []oauth2.AuthCodeOption

//WithCodeChallenge sets the `code_challenge` params in the auth request
func WithCodeChallenge(codeChallenge string) AuthURLOpt {
	return func() []oauth2.AuthCodeOption {
		return []oauth2.AuthCodeOption{
			oauth2.SetAuthURLParam("code_challenge", codeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		}
	}
}

type CodeExchangeOpt func() []oauth2.AuthCodeOption

//WithCodeVerifier sets the `code_verifier` param in the token request
func WithCodeVerifier(codeVerifier string) CodeExchangeOpt {
	return func() []oauth2.AuthCodeOption {
		return []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("code_verifier", codeVerifier)}
	}
}

func isOpenID(scopes []string) bool {
	for _, scope := range scopes {
		if scope == oidc.ScopeOpenID {
			return true
		}
	}
	return false
}

//deprecated: use Configuration instead
type Config struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	Issuer       string
	Scopes       []string
	Endpoints    oauth2.Endpoint
}
