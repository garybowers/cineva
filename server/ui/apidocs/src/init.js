// The API reference: Swagger UI over the spec the server embeds. Requests
// made with "Try it out" are same-origin, so they carry the browser's
// sign-in cookie; an API key can be given under "Authorize" instead.
window.ui = SwaggerUIBundle({
  url: '/api/v1/openapi.yaml',
  dom_id: '#swagger-ui',
  presets: [SwaggerUIBundle.presets.apis],
  layout: 'BaseLayout',
  deepLinking: true,
  withCredentials: true,
  // Off: by default Swagger UI sends the spec to validator.swagger.io, and
  // Cineva does not phone home.
  validatorUrl: null,
  docExpansion: 'list',
  defaultModelsExpandDepth: 0,
  displayRequestDuration: true,
  filter: true,
})
