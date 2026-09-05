package store

import "github.com/zhong/droply/internal/model"

type Store interface {
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
	CreateOrUpdateAccessRule(subdomainID int64, projectID *int64, allowedIPs []string, passwordHash string, sessionTTL int, weWorkEnabled bool, allowedWeWorkUsers []string) (*model.AccessRule, error)
	GetAccessRule(subdomainID int64, projectID *int64) (*model.AccessRule, error)
	DeleteAccessRule(subdomainID int64, projectID *int64) error
	FindAccessRuleForSite(subdomainName string, projectName string) (*model.AccessRule, error)
	HasAccessRules(subdomainID int64) (bool, error)
	RecordVisit(subdomainID int64, project, path, ip, referer, userAgent string) error
	GetPageStats(subdomainID int64, project, period string) ([]model.PageDailyStat, error)
	GetVisitLogs(subdomainID int64, project string, limit, offset int, pathFilter string) ([]model.VisitLog, int, error)
	CleanupVisitLogs(retentionDays int) (int64, error)
	Close() error
}
