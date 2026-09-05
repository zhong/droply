package server

import (
	"context"
	"net/http"
	"slices"

	"github.com/zhong/droply/internal/model"
)

// managementOperation is declared at the route. Authorization and auditing never
// infer policy from URL spelling or an HTTP method.
type managementOperation uint8

const (
	opUnknown managementOperation = iota
	opMe
	opAccessibleProjects
	opListInvitations
	opCreateInvitation
	opRevokeInvitation
	opCreateSubdomain
	opListSubdomains
	opCertificateStatus
	opDeleteSubdomain
	opListProjects
	opDeleteProject
	opDeploy
	opListDeployments
	opRollback
	opPromote
	opPublicationEvents
	opListMembers
	opPutMember
	opRemoveMember
	opListProjectTokens
	opCreateProjectToken
	opRevokeProjectToken
	opCreateDomain
	opListDomains
	opDeleteDomain
	opVerifyDomain
	opSetSubdomainAccess
	opGetSubdomainAccess
	opDeleteSubdomainAccess
	opSetProjectAccess
	opGetProjectAccess
	opDeleteProjectAccess
	opGetStats
	opGetLogs
	opProjectAudit
	opAdminAudit
	opPreviewCleanup
	opApplyCleanup
)

const operationContextKey contextKey = "management-operation"

func operationFromRequest(r *http.Request) managementOperation {
	op, _ := r.Context().Value(operationContextKey).(managementOperation)
	return op
}

func (s *Server) forOperation(op managementOperation) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		authenticated := s.authMiddleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), operationContextKey, op)
			authenticated.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type operationPolicy struct {
	role  string
	token string // empty forbids project credentials; read/deploy/production are finite cases below.
	audit string
}

func (op managementOperation) policy() operationPolicy {
	switch op {
	case opListDeployments, opPublicationEvents:
		return operationPolicy{role: "viewer", token: "read"}
	case opListMembers, opListDomains, opGetProjectAccess, opGetStats, opGetLogs, opProjectAudit, opPreviewCleanup:
		return operationPolicy{role: "viewer"}
	case opListProjectTokens:
		return operationPolicy{role: "deployer"}
	case opDeploy:
		return operationPolicy{role: "deployer", token: "deploy", audit: "deployment.create"}
	case opPromote:
		return operationPolicy{role: "deployer", token: "production", audit: "deployment.promote"}
	case opRollback:
		return operationPolicy{role: "deployer", token: "production", audit: "deployment.rollback"}
	case opCreateProjectToken:
		return operationPolicy{role: "deployer", audit: "token.create"}
	case opRevokeProjectToken:
		return operationPolicy{role: "deployer", audit: "token.revoke"}
	case opPutMember:
		return operationPolicy{role: "owner", audit: "member.set"}
	case opRemoveMember:
		return operationPolicy{role: "owner", audit: "member.remove"}
	case opCreateDomain:
		return operationPolicy{role: "owner", audit: "domain.create"}
	case opVerifyDomain:
		return operationPolicy{role: "owner", audit: "domain.verify"}
	case opDeleteDomain:
		return operationPolicy{role: "owner", audit: "domain.remove"}
	case opSetProjectAccess:
		return operationPolicy{role: "owner", audit: "access.set"}
	case opDeleteProjectAccess:
		return operationPolicy{role: "owner", audit: "access.remove"}
	case opDeleteProject:
		return operationPolicy{role: "owner", audit: "project.remove"}
	case opApplyCleanup:
		return operationPolicy{role: "owner", audit: "deployment.cleanup"}
	case opSetSubdomainAccess:
		return operationPolicy{audit: "subdomain.access.set"}
	case opDeleteSubdomainAccess:
		return operationPolicy{audit: "subdomain.access.remove"}
	case opCreateInvitation:
		return operationPolicy{audit: "invitation.create"}
	case opRevokeInvitation:
		return operationPolicy{audit: "invitation.revoke"}
	default:
		// Account/subdomain/administrator operations retain their dedicated guards.
		// They never admit a project credential.
		return operationPolicy{}
	}
}

func (op managementOperation) tokenAllows(r *http.Request, token *model.ProjectToken) bool {
	scope := op.policy().token
	switch scope {
	case "read":
		return true
	case "deploy":
		values, present := r.URL.Query()["environment"]
		if present && (len(values) != 1 || values[0] == "") {
			return false
		}
		scope = "production"
		if present {
			scope = values[0]
		}
		if scope != "production" && scope != "preview" {
			return false
		}
	case "production":
	default:
		return false
	}
	return slices.Contains(token.Scopes, scope)
}
