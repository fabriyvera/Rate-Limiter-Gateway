# Arquitectura v1 y hallazgos de revisión

Flujo: cliente -> proxy HTTP -> interfaz Limiter -> motor en memoria -> upstream.
cmd/gateway compone configuración, ciclo de vida y componentes. internal/limiter
contiene estado y políticas; internal/proxy adapta sus resultados a HTTP.
cmd/upstream es únicamente un servicio de prueba.

Hallazgos corregidos:
- módulo e imports conservaban tuusuario;
- entrypoint, proxy, scripts k6 y configuración de despliegue estaban vacíos;
- Fixed Window iniciaba ventanas por cliente y no reiniciaba en igualdad exacta;
- Sliding Counter truncaba fracciones y admitía solicitudes sin espacio completo;
- mapas no eliminaban clientes inactivos ni limitaban cardinalidad;
- pruebas dependían solo del reloj real, sin verificar concurrencia;
- varios algoritmos no informaban ResetAt.

Se conserva la interfaz y los cuatro constructores públicos. El motor comparte bloqueo,
reloj, validación y ciclo de vida para evitar cuatro implementaciones divergentes de
infraestructura. Las políticas están separadas en ramas explícitas. El mutex global es
una limitación conocida a medir; no se afirma rendimiento de producción.

Se retiraron placeholders vacíos de dashboard, almacenamiento y YAML. Los ADR originales
se conservan como contexto histórico; ADR 004 delimita qué está implementado.
No hay autenticación, TLS, límites distribuidos, reglas dinámicas ni protección DDoS integral.
No exponer el laboratorio directamente a Internet. Los endpoints de diagnóstico comparten
el listener local y no consumen cuota.
