package validation

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"k8s.io/kubernetes/pkg/api/validation"
	"k8s.io/kubernetes/pkg/serviceaccount"
	"k8s.io/kubernetes/pkg/util/validation/field"

	oapi "github.com/openshift/origin/pkg/api"
	authorizerscopes "github.com/openshift/origin/pkg/authorization/authorizer/scope"
	"github.com/openshift/origin/pkg/oauth/api"
	uservalidation "github.com/openshift/origin/pkg/user/api/validation"
)

const MinTokenLength = 32

// PKCE [RFC7636] code challenge methods supported
// https://tools.ietf.org/html/rfc7636#section-4.3
const (
	codeChallengeMethodPlain  = "plain"
	codeChallengeMethodSHA256 = "S256"
)

var CodeChallengeMethodsSupported = []string{codeChallengeMethodPlain, codeChallengeMethodSHA256}

func ValidateTokenName(name string, prefix bool) []string {
	if reasons := oapi.MinimalNameRequirements(name, prefix); len(reasons) != 0 {
		return reasons
	}

	if len(name) < MinTokenLength {
		return []string{fmt.Sprintf("must be at least %d characters long", MinTokenLength)}
	}
	return nil
}

func ValidateRedirectURI(redirect string) (bool, string) {
	if len(redirect) == 0 {
		return true, ""
	}

	u, err := url.Parse(redirect)
	if err != nil {
		return false, err.Error()
	}
	if len(u.Fragment) != 0 {
		return false, "may not contain a fragment"
	}
	for _, s := range strings.Split(u.Path, "/") {
		if s == "." {
			return false, "may not contain a path segment of ."
		}
		if s == ".." {
			return false, "may not contain a path segment of .."
		}
	}
	return true, ""
}

func ValidateAccessToken(accessToken *api.OAuthAccessToken) field.ErrorList {
	allErrs := validation.ValidateObjectMeta(&accessToken.ObjectMeta, false, ValidateTokenName, field.NewPath("metadata"))
	allErrs = append(allErrs, ValidateClientNameField(accessToken.ClientName, field.NewPath("clientName"))...)
	allErrs = append(allErrs, ValidateUserNameField(accessToken.UserName, field.NewPath("userName"))...)
	allErrs = append(allErrs, ValidateScopes(accessToken.Scopes, field.NewPath("scopes"))...)

	if len(accessToken.UserUID) == 0 {
		allErrs = append(allErrs, field.Required(field.NewPath("userUID"), ""))
	}
	if ok, msg := ValidateRedirectURI(accessToken.RedirectURI); !ok {
		allErrs = append(allErrs, field.Invalid(field.NewPath("redirectURI"), accessToken.RedirectURI, msg))
	}

	return allErrs
}

func ValidateAccessTokenUpdate(newToken, oldToken *api.OAuthAccessToken) field.ErrorList {
	allErrs := validation.ValidateObjectMetaUpdate(&newToken.ObjectMeta, &oldToken.ObjectMeta, field.NewPath("metadata"))
	copied := *oldToken
	copied.ObjectMeta = newToken.ObjectMeta
	return append(allErrs, validation.ValidateImmutableField(newToken, &copied, field.NewPath(""))...)
}

var codeChallengeRegex = regexp.MustCompile("^[a-zA-Z0-9._~-]{43,128}$")

func ValidateAuthorizeToken(authorizeToken *api.OAuthAuthorizeToken) field.ErrorList {
	allErrs := validation.ValidateObjectMeta(&authorizeToken.ObjectMeta, false, ValidateTokenName, field.NewPath("metadata"))
	allErrs = append(allErrs, ValidateClientNameField(authorizeToken.ClientName, field.NewPath("clientName"))...)
	allErrs = append(allErrs, ValidateUserNameField(authorizeToken.UserName, field.NewPath("userName"))...)
	allErrs = append(allErrs, ValidateScopes(authorizeToken.Scopes, field.NewPath("scopes"))...)

	if len(authorizeToken.UserUID) == 0 {
		allErrs = append(allErrs, field.Required(field.NewPath("userUID"), ""))
	}
	if ok, msg := ValidateRedirectURI(authorizeToken.RedirectURI); !ok {
		allErrs = append(allErrs, field.Invalid(field.NewPath("redirectURI"), authorizeToken.RedirectURI, msg))
	}

	if len(authorizeToken.CodeChallenge) > 0 || len(authorizeToken.CodeChallengeMethod) > 0 {
		switch {
		case len(authorizeToken.CodeChallenge) == 0:
			allErrs = append(allErrs, field.Required(field.NewPath("codeChallenge"), "required if codeChallengeMethod is specified"))
		case !codeChallengeRegex.MatchString(authorizeToken.CodeChallenge):
			allErrs = append(allErrs, field.Invalid(field.NewPath("codeChallenge"), authorizeToken.CodeChallenge, "must be 43-128 characters [a-zA-Z0-9.~_-]"))
		}

		switch authorizeToken.CodeChallengeMethod {
		case "":
			allErrs = append(allErrs, field.Required(field.NewPath("codeChallengeMethod"), "required if codeChallenge is specified"))
		case codeChallengeMethodPlain, codeChallengeMethodSHA256:
			// no-op, good
		default:
			allErrs = append(allErrs, field.NotSupported(field.NewPath("codeChallengeMethod"), authorizeToken.CodeChallengeMethod, CodeChallengeMethodsSupported))
		}
	}

	return allErrs
}

func ValidateAuthorizeTokenUpdate(newToken, oldToken *api.OAuthAuthorizeToken) field.ErrorList {
	allErrs := validation.ValidateObjectMetaUpdate(&newToken.ObjectMeta, &oldToken.ObjectMeta, field.NewPath("metadata"))
	copied := *oldToken
	copied.ObjectMeta = newToken.ObjectMeta
	return append(allErrs, validation.ValidateImmutableField(newToken, &copied, field.NewPath(""))...)
}

func ValidateClient(client *api.OAuthClient) field.ErrorList {
	allErrs := validation.ValidateObjectMeta(&client.ObjectMeta, false, validation.NameIsDNSSubdomain, field.NewPath("metadata"))
	for i, redirect := range client.RedirectURIs {
		if ok, msg := ValidateRedirectURI(redirect); !ok {
			allErrs = append(allErrs, field.Invalid(field.NewPath("redirectURIs").Index(i), redirect, msg))
		}
	}

	for i, restriction := range client.ScopeRestrictions {
		allErrs = append(allErrs, ValidateScopeRestriction(restriction, field.NewPath("scopeRestrictions").Index(i))...)
	}

	return allErrs
}

func ValidateScopeRestriction(restriction api.ScopeRestriction, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	specifiers := 0
	if len(restriction.ExactValues) > 0 {
		specifiers = specifiers + 1
	}
	if restriction.ClusterRole != nil {
		specifiers = specifiers + 1
	}
	if specifiers != 1 {
		allErrs = append(allErrs, field.Invalid(fldPath, restriction, "exactly one of literals, clusterRole is required"))
		return allErrs
	}

	switch {
	case len(restriction.ExactValues) > 0:
		for i, literal := range restriction.ExactValues {
			if len(literal) == 0 {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("literals").Index(i), literal, "may not be empty"))
			}
		}

	case restriction.ClusterRole != nil:
		if len(restriction.ClusterRole.RoleNames) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("clusterRole", "roleNames"), "won't match anything"))
		}
		if len(restriction.ClusterRole.Namespaces) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("clusterRole", "namespaces"), "won't match anything"))
		}
	}

	return allErrs
}

func ValidateClientUpdate(client *api.OAuthClient, oldClient *api.OAuthClient) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateClient(client)...)
	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(&client.ObjectMeta, &oldClient.ObjectMeta, field.NewPath("metadata"))...)

	return allErrs
}

func ValidateClientAuthorizationName(name string, prefix bool) []string {
	if reasons := oapi.MinimalNameRequirements(name, prefix); len(reasons) != 0 {
		return reasons
	}

	lastColon := strings.Index(name, ":")
	if lastColon <= 0 || lastColon >= len(name)-1 {
		return []string{"must be in the format <userName>:<clientName>"}
	}

	return nil
}

func ValidateClientAuthorization(clientAuthorization *api.OAuthClientAuthorization) field.ErrorList {
	allErrs := field.ErrorList{}

	expectedName := fmt.Sprintf("%s:%s", clientAuthorization.UserName, clientAuthorization.ClientName)

	metadataErrs := validation.ValidateObjectMeta(&clientAuthorization.ObjectMeta, false, ValidateClientAuthorizationName, field.NewPath("metadata"))
	if len(metadataErrs) > 0 {
		allErrs = append(allErrs, metadataErrs...)
	} else if clientAuthorization.Name != expectedName {
		allErrs = append(allErrs, field.Invalid(field.NewPath("metadata", "name"), clientAuthorization.Name, "must be in the format <userName>:<clientName>"))
	}

	allErrs = append(allErrs, ValidateClientNameField(clientAuthorization.ClientName, field.NewPath("clientName"))...)
	allErrs = append(allErrs, ValidateUserNameField(clientAuthorization.UserName, field.NewPath("userName"))...)
	allErrs = append(allErrs, ValidateScopes(clientAuthorization.Scopes, field.NewPath("scopes"))...)

	if len(clientAuthorization.UserUID) == 0 {
		allErrs = append(allErrs, field.Required(field.NewPath("useruid"), ""))
	}

	return allErrs
}

func ValidateClientAuthorizationUpdate(newAuth *api.OAuthClientAuthorization, oldAuth *api.OAuthClientAuthorization) field.ErrorList {
	allErrs := ValidateClientAuthorization(newAuth)

	allErrs = append(allErrs, validation.ValidateObjectMetaUpdate(&newAuth.ObjectMeta, &oldAuth.ObjectMeta, field.NewPath("metadata"))...)

	if oldAuth.ClientName != newAuth.ClientName {
		allErrs = append(allErrs, field.Invalid(field.NewPath("clientName"), newAuth.ClientName, "clientName is not a mutable field"))
	}
	if oldAuth.UserName != newAuth.UserName {
		allErrs = append(allErrs, field.Invalid(field.NewPath("userName"), newAuth.UserName, "userName is not a mutable field"))
	}
	if oldAuth.UserUID != newAuth.UserUID {
		allErrs = append(allErrs, field.Invalid(field.NewPath("userUID"), newAuth.UserUID, "userUID is not a mutable field"))
	}

	return allErrs
}

func ValidateClientNameField(value string, fldPath *field.Path) field.ErrorList {
	if len(value) == 0 {
		return field.ErrorList{field.Required(fldPath, "")}
	} else if _, saName, err := serviceaccount.SplitUsername(value); err == nil {
		if reasons := validation.ValidateServiceAccountName(saName, false); len(reasons) != 0 {
			return field.ErrorList{field.Invalid(fldPath, value, strings.Join(reasons, ", "))}
		}
	} else if reasons := validation.NameIsDNSSubdomain(value, false); len(reasons) != 0 {
		return field.ErrorList{field.Invalid(fldPath, value, strings.Join(reasons, ", "))}
	}
	return field.ErrorList{}
}

func ValidateUserNameField(value string, fldPath *field.Path) field.ErrorList {
	if len(value) == 0 {
		return field.ErrorList{field.Required(fldPath, "")}
	} else if reasons := uservalidation.ValidateUserName(value, false); len(reasons) != 0 {
		return field.ErrorList{field.Invalid(fldPath, value, strings.Join(reasons, ", "))}
	}
	return field.ErrorList{}
}

func ValidateScopes(scopes []string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, scope := range scopes {
		illegalCharacter := false
		// https://tools.ietf.org/html/rfc6749#section-3.3 (full list of allowed chars is %x21 / %x23-5B / %x5D-7E)
		// for those without an ascii table, that's `!`, `#-[`, `]-~` inclusive.
		for _, ch := range scope {
			switch {
			case ch == '!':
			case ch >= '#' && ch <= '[':
			case ch >= ']' && ch <= '~':
			default:
				allErrs = append(allErrs, field.Invalid(fldPath.Index(i), scope, fmt.Sprintf("%v not allowed", ch)))
				illegalCharacter = true
			}
		}
		if illegalCharacter {
			continue
		}

		found := false
		for _, evaluator := range authorizerscopes.ScopeEvaluators {
			if !evaluator.Handles(scope) {
				continue
			}

			found = true
			if err := evaluator.Validate(scope); err != nil {
				allErrs = append(allErrs, field.Invalid(fldPath.Index(i), scope, err.Error()))
				break
			}
		}

		if !found {
			allErrs = append(allErrs, field.Invalid(fldPath.Index(i), scope, "no scope handler found"))
		}
	}

	return allErrs
}
