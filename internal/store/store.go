package store

import (
	"context"
	"github.com/zhong/droply/internal/model"
)

type Store interface {
	ConsoleSessionStore
	AuditStore
	IdentityStore
	MembershipStore
	ProjectTokenStore
	GetSiteTarget(context.Context, string) (*model.SiteTarget, error)
	BeginDeploymentTarget(context.Context, int64, string, string, string, string) (*model.Deployment, error)
	PromoteDeployment(context.Context, int64, int, int64) (*model.Deployment, bool, error)
	ListPublicationEvents(context.Context, int64) ([]model.PublicationEvent, error)
	CreateUser(email, hashedPassword, apiToken string) (*model.User, error)
	GetUserByEmail(email string) (*model.User, error)
	GetUserByToken(token string) (*model.User, error)
	CreateSubdomain(userID int64, name string) (*model.Subdomain, error)
	ListSubdomains(userID int64) ([]model.Subdomain, error)
	GetSubdomainByName(name string) (*model.Subdomain, error)
	DeleteSubdomain(userID int64, name string) error
	CreateProject(subdomainID int64, name string) (*model.Project, error)
	GetProject(subdomainID int64, name string) (*model.Project, error)
	ListProjects(subdomainID int64) ([]model.Project, error)
	DeleteProject(subdomainID int64, name string) error
	BeginDeployment(context.Context, int64, string) (*model.Deployment, error)
	CommitDeployment(context.Context, int64, int, int64, string) error
	FailDeployment(context.Context, int64, string) error
	GetActiveDeployment(context.Context, int64) (*model.Deployment, error)
	GetDeployment(context.Context, int64, int) (*model.Deployment, error)
	GetDeploymentByID(context.Context, int64) (*model.Deployment, error)
	ListAllDeployments(context.Context) ([]model.Deployment, error)
	AttachLegacyArtifact(context.Context, int64, string, int, int64, string) error
	SwitchDeployment(context.Context, int64, int) (*model.Deployment, bool, error)
	DeploymentReferenced(context.Context, int64) (bool, error)
	MarkArtifactDeleting(context.Context, int64) error
	SetArtifactState(context.Context, int64, string) error
	PutDeploymentReference(context.Context, int64, string, int64) error
	CreateDeployment(projectID int64, fileCount int, totalSize int64) (*model.Deployment, error)
	ActivateDeployment(deploymentID int64) error
	ListDeployments(projectID int64) ([]model.Deployment, error)
	CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error)
	GetCustomDomain(domain string) (*model.CustomDomain, error)
	VerifyCustomDomain(domain string) error
	VerifyCustomDomainChallenge(domain, token string) error
	ListCustomDomains(projectID int64) ([]model.CustomDomain, error)
	DeleteCustomDomain(projectID int64, domain string) error
	ListAllSubdomains() ([]model.Subdomain, error)
	ListAllVerifiedDomainsWithPaths() ([]model.DomainWithPath, error)
	PutAccessRule(context.Context, AccessRuleInput) (*model.AccessRule, error)
	GetAccessRule(ctx context.Context, subdomainID int64, projectID *int64) (*model.AccessRule, error)
	DeleteAccessRule(ctx context.Context, subdomainID int64, projectID *int64) error
	FindAccessRuleForSite(ctx context.Context, subdomainName string, projectName string) (*model.AccessRule, error)
	HasAccessRules(ctx context.Context, subdomainID int64) (bool, error)
	RecordVisit(subdomainID int64, project, path, ip, referer, userAgent string) error
	GetPageStats(subdomainID int64, project, period string) ([]model.PageDailyStat, error)
	GetVisitLogs(subdomainID int64, project string, limit, offset int, pathFilter string) ([]model.VisitLog, int, error)
	CleanupVisitLogs(retentionDays int) (int64, error)
	Close() error
}
