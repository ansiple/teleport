/*
Copyright 2015 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package services

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"text/template"
	"time"

	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/utils"

	log "github.com/Sirupsen/logrus"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"
)

// SAMLConnector specifies configuration for SAML 2.0 dentity providers
type SAMLConnector interface {
	// Resource provides common methods for objects
	Resource
	// GetDisplay returns display - friendly name for this provider.
	GetDisplay() string
	// SetDisplay sets friendly name for this provider.
	SetDisplay(string)
	// GetAttributesToRoles returns attributes to roles mapping
	GetAttributesToRoles() []AttributeMapping
	// SetAttributesToRoles sets attributes to roles mapping
	SetAttributesToRoles(mapping []AttributeMapping)
	// GetAttributes returns list of attributes expected by mappings
	GetAttributes() []string
	// MapAttributes maps attributes to roles
	MapAttributes(assertionInfo saml2.AssertionInfo) []string
	// RoleFromTemplate creates a role from a template and claims.
	RoleFromTemplate(assertionInfo saml2.AssertionInfo) (Role, error)
	// Check checks SAML connector for errors
	CheckAndSetDefaults() error
	// SetIssuer sets issuer
	SetIssuer(issuer string)
	// GetIssuer returns issuer
	GetIssuer() string
	// GetSigningKeyPair returns signing key pair
	GetSigningKeyPair() *SigningKeyPair
	// GetSigningKeyPair sets signing key pair
	SetSigningKeyPair(k *SigningKeyPair)
	// Equals returns true if the connectors are identical
	Equals(other SAMLConnector) bool
	// GetSSO returns SSO service
	GetSSO() string
	// SetSSO sets SSO service
	SetSSO(string)
	// GetEntityDescriptor returns XML entity descriptor of the service
	GetEntityDescriptor() string
	// SetEntityDescriptor sets entity descritor of the service
	SetEntityDescriptor(v string)
	// GetCert returns identity provider checking x509 certificate
	GetCert() string
	// SetCert sets identity provider checking certificate
	SetCert(string)
	// GetServiceProviderIssuer returns service provider issuer
	GetServiceProviderIssuer() string
	// SetServiceProviderIssuer sets service provider issuer
	SetServiceProviderIssuer(v string)
	// GetAudience returns audience
	GetAudience() string
	// SetAudience sets audience
	SetAudience(v string)
	// GetServiceProvider initialises service provider spec from settings
	GetServiceProvider() (*saml2.SAMLServiceProvider, error)
	// GetAssertionConsumerService returns assertion consumer service URL
	GetAssertionConsumerService() string
	// SetAssertionConsumerService sets assertion consumer service URL
	SetAssertionConsumerService(v string)
}

// NewSAMLConnector returns a new SAMLConnector based off a name and SAMLConnectorSpecV2.
func NewSAMLConnector(name string, spec SAMLConnectorSpecV2) SAMLConnector {
	return &SAMLConnectorV2{
		Kind:    KindSAMLConnector,
		Version: V2,
		Metadata: Metadata{
			Name:      name,
			Namespace: defaults.Namespace,
		},
		Spec: spec,
	}
}

var samlConnectorMarshaler SAMLConnectorMarshaler = &TeleportSAMLConnectorMarshaler{}

// SetSAMLConnectorMarshaler sets global user marshaler
func SetSAMLConnectorMarshaler(m SAMLConnectorMarshaler) {
	marshalerMutex.Lock()
	defer marshalerMutex.Unlock()
	samlConnectorMarshaler = m
}

// GetSAMLConnectorMarshaler returns currently set user marshaler
func GetSAMLConnectorMarshaler() SAMLConnectorMarshaler {
	marshalerMutex.RLock()
	defer marshalerMutex.RUnlock()
	return samlConnectorMarshaler
}

// SAMLConnectorMarshaler implements marshal/unmarshal of User implementations
// mostly adds support for extended versions
type SAMLConnectorMarshaler interface {
	// UnmarshalSAMLConnector unmarshals connector from binary representation
	UnmarshalSAMLConnector(bytes []byte) (SAMLConnector, error)
	// MarshalSAMLConnector marshals connector to binary representation
	MarshalSAMLConnector(c SAMLConnector, opts ...MarshalOption) ([]byte, error)
}

// GetSAMLConnectorSchema returns schema for SAMLConnector
func GetSAMLConnectorSchema() string {
	return fmt.Sprintf(SAMLConnectorV2SchemaTemplate, MetadataSchema, SAMLConnectorSpecV2Schema)
}

type TeleportSAMLConnectorMarshaler struct{}

// UnmarshalSAMLConnector unmarshals connector from
func (*TeleportSAMLConnectorMarshaler) UnmarshalSAMLConnector(bytes []byte) (SAMLConnector, error) {
	var h ResourceHeader
	err := json.Unmarshal(bytes, &h)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	switch h.Version {
	case V2:
		var c SAMLConnectorV2
		if err := utils.UnmarshalWithSchema(GetSAMLConnectorSchema(), &c, bytes); err != nil {
			return nil, trace.BadParameter(err.Error())
		}
		return &c, nil
	}

	return nil, trace.BadParameter("SAML connector resource version %v is not supported", h.Version)
}

// MarshalUser marshals SAML connector into JSON
func (*TeleportSAMLConnectorMarshaler) MarshalSAMLConnector(c SAMLConnector, opts ...MarshalOption) ([]byte, error) {
	cfg, err := collectOptions(opts)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	type connv2 interface {
		V2() *SAMLConnectorV2
	}
	version := cfg.GetVersion()
	switch version {
	case V2:
		v, ok := c.(connv2)
		if !ok {
			return nil, trace.BadParameter("don't know how to marshal %v", V2)
		}
		return json.Marshal(v.V2())
	default:
		return nil, trace.BadParameter("version %v is not supported", version)
	}
}

// SAMLConnectorV2 is version 1 resource spec for SAML connector
type SAMLConnectorV2 struct {
	// Kind is a resource kind
	Kind string `json:"kind"`
	// Version is version
	Version string `json:"version"`
	// Metadata is connector metadata
	Metadata Metadata `json:"metadata"`
	// Spec contains connector specification
	Spec SAMLConnectorSpecV2 `json:"spec"`
}

// GetServiceProviderIssuer returns service provider issuer
func (o *SAMLConnectorV2) GetServiceProviderIssuer() string {
	return o.Spec.ServiceProviderIssuer
}

// SetServiceProviderIssuer sets service provider issuer
func (o *SAMLConnectorV2) SetServiceProviderIssuer(v string) {
	o.Spec.ServiceProviderIssuer = v
}

// GetAudience returns audience
func (o *SAMLConnectorV2) GetAudience() string {
	return o.Spec.Audience
}

// SetAudience sets audience
func (o *SAMLConnectorV2) SetAudience(v string) {
	o.Spec.Audience = v
}

// GetCert returns identity provider checking x509 certificate
func (o *SAMLConnectorV2) GetCert() string {
	return o.Spec.Cert
}

// SetCert sets identity provider checking certificate
func (o *SAMLConnectorV2) SetCert(cert string) {
	o.Spec.Cert = cert
}

// GetSSO returns SSO service
func (o *SAMLConnectorV2) GetSSO() string {
	return o.Spec.SSO
}

// SetSSO sets SSO service
func (o *SAMLConnectorV2) SetSSO(sso string) {
	o.Spec.SSO = sso
}

// GetEntityDescriptor returns XML entity descriptor of the service
func (o *SAMLConnectorV2) GetEntityDescriptor() string {
	return o.Spec.EntityDescriptor
}

// SetEntityDescriptor sets entity descritor of the service
func (o *SAMLConnectorV2) SetEntityDescriptor(v string) {
	o.Spec.EntityDescriptor = v
}

// GetAssertionConsumerService returns assertion consumer service URL
func (o *SAMLConnectorV2) GetAssertionConsumerService() string {
	return o.Spec.AssertionConsumerService
}

// SetAssertionConsumerService sets assertion consumer service URL
func (o *SAMLConnectorV2) SetAssertionConsumerService(v string) {
	o.Spec.AssertionConsumerService = v
}

// Equals returns true if the connectors are identical
func (o *SAMLConnectorV2) Equals(other SAMLConnector) bool {
	if o.GetName() != other.GetName() {
		return false
	}
	if o.GetCert() != other.GetCert() {
		return false
	}
	if o.GetAudience() != other.GetAudience() {
		return false
	}
	if o.GetEntityDescriptor() != other.GetEntityDescriptor() {
		return false
	}
	if o.Expiry() != other.Expiry() {
		return false
	}
	if o.GetIssuer() != other.GetIssuer() {
		return false
	}
	if (o.GetSigningKeyPair() == nil && other.GetSigningKeyPair() != nil) || (o.GetSigningKeyPair() != nil && other.GetSigningKeyPair() == nil) {
		return false
	}
	if o.GetSigningKeyPair() != nil {
		a, b := o.GetSigningKeyPair(), other.GetSigningKeyPair()
		if a.Cert != b.Cert || a.PrivateKey != b.PrivateKey {
			return false
		}
	}
	mappings := o.GetAttributesToRoles()
	otherMappings := other.GetAttributesToRoles()
	if len(mappings) != len(otherMappings) {
		return false
	}
	for i := range mappings {
		a, b := mappings[i], otherMappings[i]
		if a.Name != b.Name || a.Value != b.Value || !utils.StringSlicesEqual(a.Roles, b.Roles) {
			return false
		}
		if (a.RoleTemplate != nil && b.RoleTemplate == nil) || (a.RoleTemplate == nil && b.RoleTemplate != nil) {
			return false
		}
		if a.RoleTemplate != nil && !a.RoleTemplate.Equals(b.RoleTemplate) {
			return false
		}
	}
	if o.GetSSO() != other.GetSSO() {
		return false
	}
	return true
}

// V2 returns V2 version of the resource
func (o *SAMLConnectorV2) V2() *SAMLConnectorV2 {
	return o
}

// SetDisplay sets friendly name for this provider.
func (o *SAMLConnectorV2) SetDisplay(display string) {
	o.Spec.Display = display
}

// GetMetadata returns object metadata
func (o *SAMLConnectorV2) GetMetadata() Metadata {
	return o.Metadata
}

// SetExpiry sets expiry time for the object
func (o *SAMLConnectorV2) SetExpiry(expires time.Time) {
	o.Metadata.SetExpiry(expires)
}

// Expires retuns object expiry setting
func (o *SAMLConnectorV2) Expiry() time.Time {
	return o.Metadata.Expiry()
}

// SetTTL sets Expires header using realtime clock
func (o *SAMLConnectorV2) SetTTL(clock clockwork.Clock, ttl time.Duration) {
	o.Metadata.SetTTL(clock, ttl)
}

// GetName returns the name of the connector
func (o *SAMLConnectorV2) GetName() string {
	return o.Metadata.GetName()
}

// SetName sets client secret to some value
func (o *SAMLConnectorV2) SetName(name string) {
	o.Metadata.SetName(name)
}

// SetIssuer sets issuer
func (o *SAMLConnectorV2) SetIssuer(issuer string) {
	o.Spec.Issuer = issuer
}

// GetIssuer returns issuer
func (o *SAMLConnectorV2) GetIssuer() string {
	return o.Spec.Issuer
}

// Display - Friendly name for this provider.
func (o *SAMLConnectorV2) GetDisplay() string {
	if o.Spec.Display != "" {
		return o.Spec.Display
	}
	return o.GetName()
}

// GetAttributesToRoles returns attributes to roles mapping
func (o *SAMLConnectorV2) GetAttributesToRoles() []AttributeMapping {
	return o.Spec.AttributesToRoles
}

// SetAttributesToRoles sets attributes to roles mapping
func (o *SAMLConnectorV2) SetAttributesToRoles(mapping []AttributeMapping) {
	o.Spec.AttributesToRoles = mapping
}

// GetAttributes returns list of attributes expected by mappings
func (o *SAMLConnectorV2) GetAttributes() []string {
	var out []string
	for _, mapping := range o.Spec.AttributesToRoles {
		out = append(out, mapping.Name)
	}
	return utils.Deduplicate(out)
}

// MapClaims maps claims to roles
func (o *SAMLConnectorV2) MapAttributes(assertionInfo saml2.AssertionInfo) []string {
	var roles []string
	for _, mapping := range o.Spec.AttributesToRoles {
		for _, attr := range assertionInfo.Values {
			if attr.Name != mapping.Name {
				continue
			}
			for _, value := range attr.Values {
				fmt.Printf("attr.Name: %v, attr.Value: %v, mapping.Name: %v, mapping.Value: %v\n", attr.Name, attr.Values, mapping.Name, mapping.Value)
				if value.Value == mapping.Value {
					roles = append(roles, mapping.Roles...)
				}
			}
		}
	}
	return utils.Deduplicate(roles)
}

func executeSAMLStringTemplate(raw string, assertionInfo saml2.AssertionInfo) (string, error) {
	tmpl, err := template.New("dynamic-roles").Parse(raw)
	if err != nil {
		return "", trace.Wrap(err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, assertionInfo)
	if err != nil {
		return "", trace.Wrap(err)
	}

	return buf.String(), nil
}

func executeSAMLSliceTemplate(raw []string, assertionInfo saml2.AssertionInfo) ([]string, error) {
	var sl []string

	for _, v := range raw {
		tmpl, err := template.New("dynamic-roles").Parse(v)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		var buf bytes.Buffer
		err = tmpl.Execute(&buf, assertionInfo)
		if err != nil {
			return nil, trace.Wrap(err)
		}

		sl = append(sl, buf.String())
	}

	return sl, nil
}

// RoleFromTemplate creates a role from a template and claims.
func (o *SAMLConnectorV2) RoleFromTemplate(assertionInfo saml2.AssertionInfo) (Role, error) {
	for _, mapping := range o.Spec.AttributesToRoles {
		for _, attr := range assertionInfo.Values {
			// attribute name doesn't match
			if attr.Name != mapping.Value {
				continue
			}

			// attribute value doesn't match
			var matched bool
			for _, val := range attr.Values {
				if val.Value == mapping.Value {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}

			// claim name and value match, if a role template exists, execute template
			roleTemplate := mapping.RoleTemplate
			if roleTemplate != nil {
				// at the moment, only allow templating for role name and logins
				executedName, err := executeSAMLStringTemplate(roleTemplate.GetName(), assertionInfo)
				if err != nil {
					return nil, trace.Wrap(err)
				}
				executedLogins, err := executeSAMLSliceTemplate(roleTemplate.GetLogins(), assertionInfo)
				if err != nil {
					return nil, trace.Wrap(err)
				}

				roleTemplate.SetName(executedName)
				roleTemplate.SetLogins(executedLogins)

				// check all fields and make sure we have have a valid role
				err = roleTemplate.CheckAndSetDefaults()
				if err != nil {
					return nil, trace.Wrap(err)
				}

				return roleTemplate, nil
			}
		}
	}

	return nil, trace.BadParameter("unable to create role from template")
}

// GetServiceProvider initialises service provider spec from settings
func (o *SAMLConnectorV2) GetServiceProvider() (*saml2.SAMLServiceProvider, error) {
	if o.Metadata.Name == "" {
		return nil, trace.BadParameter("ID: missing connector name, name your connector to refer to internally e.g. okta1")
	}
	if o.Spec.AssertionConsumerService == "" {
		return nil, trace.BadParameter("missing acs - assertion consumer service parameter, set service URL that will receive POST requests from SAML")
	}
	if o.Spec.ServiceProviderIssuer == "" {
		o.Spec.ServiceProviderIssuer = o.Spec.AssertionConsumerService
	}
	if o.Spec.Audience == "" {
		o.Spec.Audience = o.Spec.AssertionConsumerService
	}
	certStore := dsig.MemoryX509CertificateStore{
		Roots: []*x509.Certificate{},
	}
	if o.Spec.EntityDescriptor != "" {
		metadata := &types.EntityDescriptor{}
		err := xml.Unmarshal([]byte(o.Spec.EntityDescriptor), metadata)
		if err != nil {
			return nil, trace.Wrap(err, "failed to parse entity_descriptor")
		}

		for _, kd := range metadata.IDPSSODescriptor.KeyDescriptors {
			certData, err := base64.StdEncoding.DecodeString(kd.KeyInfo.X509Data.X509Certificate.Data)
			if err != nil {
				return nil, trace.Wrap(err)
			}
			cert, err := x509.ParseCertificate(certData)
			if err != nil {
				return nil, trace.Wrap(err, "failed to parse certificate in metadata")
			}
			certStore.Roots = append(certStore.Roots, cert)
		}
		o.Spec.Issuer = metadata.EntityID
		o.Spec.SSO = metadata.IDPSSODescriptor.SingleSignOnService.Location
	}
	if o.Spec.Issuer == "" {
		return nil, trace.BadParameter("no issuer or entityID set, either set issuer as a paramter or via entity_descriptor spec")
	}
	if o.Spec.SSO == "" {
		return nil, trace.BadParameter("no SSO set either explicitly or via entity_descriptor spec")
	}
	if o.Spec.Cert != "" {
		cert, err := utils.ParseCertificatePEM([]byte(o.Spec.Cert))
		if err != nil {
			return nil, trace.Wrap(err)
		}
		certStore.Roots = append(certStore.Roots, cert)
	}
	if len(certStore.Roots) == 0 {
		return nil, trace.BadParameter(
			"no identity provider certificate provided, either set certificate as a parameter or via entity_descriptor")
	}
	if o.Spec.SigningKeyPair == nil {
		keyPEM, certPEM, err := utils.GenerateSelfSignedSigningCert(pkix.Name{
			Organization: []string{"Teleport OSS"},
			CommonName:   "teleport.localhost.localdomain",
		}, nil, 10*365*24*time.Hour)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		o.Spec.SigningKeyPair = &SigningKeyPair{
			PrivateKey: string(keyPEM),
			Cert:       string(certPEM),
		}
	}
	keyStore, err := utils.ParseSigningKeyStorePEM(o.Spec.SigningKeyPair.PrivateKey, o.Spec.SigningKeyPair.Cert)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	// make sure claim mappings have either roles or a role template
	for _, v := range o.Spec.AttributesToRoles {
		hasRoles := false
		if len(v.Roles) > 0 {
			hasRoles = true
		}
		hasRoleTemplate := false
		if v.RoleTemplate != nil {
			hasRoleTemplate = true
		}

		// we either need to have roles or role templates not both or neither
		// ! ( hasRoles XOR hasRoleTemplate )
		if hasRoles == hasRoleTemplate {
			return nil, trace.BadParameter("need roles or role template (not both or none)")
		}
	}
	log.Debugf("SSO: %v\nIssuer:%v,acs:%v", o.Spec.SSO, o.Spec.Issuer, o.Spec.AssertionConsumerService)

	rac := &saml2.RequestedAuthnContext{
		Comparison: "minimum",
		Contexts:   []string{"urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport"},
	}

	sp := &saml2.SAMLServiceProvider{
		IdentityProviderSSOURL:      o.Spec.SSO,
		IdentityProviderIssuer:      o.Spec.Issuer,
		ServiceProviderIssuer:       o.Spec.ServiceProviderIssuer,
		AssertionConsumerServiceURL: o.Spec.AssertionConsumerService,
		SignAuthnRequests:           true,
		AudienceURI:                 o.Spec.Audience,
		IDPCertificateStore:         &certStore,
		SPKeyStore:                  keyStore,
		RequestedAuthnContext:       rac,
	}
	return sp, nil
}

// GetSigningKeyPair returns signing key pair
func (o *SAMLConnectorV2) GetSigningKeyPair() *SigningKeyPair {
	return o.Spec.SigningKeyPair
}

// GetSigningKeyPair sets signing key pair
func (o *SAMLConnectorV2) SetSigningKeyPair(k *SigningKeyPair) {
	o.Spec.SigningKeyPair = k
}

func (o *SAMLConnectorV2) CheckAndSetDefaults() error {
	_, err := o.GetServiceProvider()
	return err
}

// SAMLConnectorV2SchemaTemplate is a template JSON Schema for user
const SAMLConnectorV2SchemaTemplate = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "spec", "metadata", "version"],
  "properties": {
    "kind": {"type": "string"},
    "version": {"type": "string", "default": "v1"},
    "metadata": %v,
    "spec": %v
  }
}`

// SAMLConnectorSpecV2 specifies configuration for Open ID Connect compatible external
// identity provider, e.g. google in some organisation
type SAMLConnectorSpecV2 struct {
	// Issuer is identity provider issuer
	Issuer string `json:"issuer"`
	// SSO is URL of the identity provider SSO service
	SSO string `json:"sso"`
	// Cert is identity provider certificate PEM
	// IDP signs <Response> responses using this certificate
	Cert string `json:"cert"`
	// Display controls how this connector is displayed
	Display string `json:"display"`
	// AssertionConsumerService is a URL for assertion consumer service
	// on the service provider (Teleport's side)
	AssertionConsumerService string `json:"acs"`
	// Audience uniquely identifies our service provider
	Audience string `json:"audience"`
	// SertviceProviderIssuer is the issuer of the service provider (Teleport)
	ServiceProviderIssuer string `json:"service_provider_issuer"`
	// EntityDescriptor is XML with descriptor, can be used to supply configuration
	// parameters in one XML files vs supplying them in the individual elelemtns
	EntityDescriptor string `json:"entity_descriptor"`
	// AttriburesToRoles is a list of mappings of attribute statements to roles
	AttributesToRoles []AttributeMapping `json:"attributes_to_roles"`
	// SigningKeyPair is x509 key pair used to sign AuthnRequest
	SigningKeyPair *SigningKeyPair `json:"signing_key_pair,omitempty"`
}

// SAMLConnectorSpecV2Schema is a JSON Schema for SAML Connector
var SAMLConnectorSpecV2Schema = fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["acs"],
  "properties": {
    "issuer": {"type": "string"},
    "sso": {"type": "string"},
    "cert": {"type": "string"},
    "display": {"type": "string"},
    "acs": {"type": "string"},
    "audience": {"type": "string"},
    "service_provider_issuer": {"type": "string"},
    "entity_descriptor": {"type": "string"},
    "attributes_to_roles": {
      "type": "array",
      "items": %v
    },
    "signing_key_pair": %v
  }
}`, AttributeMappingSchema, SigningKeyPairSchema)

// GetAttributeNames returns a list of claim names from the claim values
func GetAttributeNames(attributes map[string]types.Attribute) []string {
	var out []string
	for _, attr := range attributes {
		out = append(out, attr.Name)
	}
	return out
}

// AttributeMapping is SAML Attribute statement mapping
// from SAML attribute statements to roles
type AttributeMapping struct {
	// Name is attribute statement name
	Name string `json:"name"`
	// Value is attribute statement value to match
	Value string `json:"value"`
	// Roles is a list of teleport roles to match
	Roles []string `json:"roles,omitempty"`
	// RoleTemplate is a template for a role that will be filled
	// with data from claims.
	RoleTemplate *RoleV2 `json:"role_template,omitempty"`
}

// AttribueMappingSchema is JSON schema for claim mapping
var AttributeMappingSchema = fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["name", "value" ],
  "properties": {
    "name": {"type": "string"},
    "value": {"type": "string"},
    "roles": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "role_template": %v
  }
}`, GetRoleSchema(""))

// SigningKeyPairSchema
var SigningKeyPairSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "private_key": {"type": "string"},
    "cert": {"type": "string"}
  }
}`

// SigningKeyPair is a key pair used to sign SAML AuthnRequest
type SigningKeyPair struct {
	// PrivateKey is PEM encoded x509 private key
	PrivateKey string `json:"private_key"`
	// Cert is certificate in OpenSSH authorized keys format
	Cert string `json:"cert"`
}
