# Protocolo de pruebas v1

No se incluyen resultados de rendimiento inventados. Registra commit, versión Go/k6,
CPU, RAM, sistema operativo, algoritmo, límite, ventana, capacidad, tasa y clientes.
Reinicia el gateway entre ejecuciones para comparar estados iniciales equivalentes.

1. Ejecuta smoke con límite 2 y ventana 2s para los cuatro algoritmos.
2. Mide upstream directo, gateway --algorithm none y cada algoritmo con idéntico entorno.
3. Ejecuta sustained y burst para uno y varios clientes, con varias repeticiones.
4. Guarda salida y exporta series: `k6 run --out json=results/run.json loadtest/sustained.js`
   (crea results antes). En PowerShell: `New-Item -ItemType Directory -Force results`.

Ejemplo PowerShell:
```powershell
$env:RPS="200"
$env:CLIENTS="1"
$env:DURATION="30s"
$env:RUN_ID="token-run-01"
k6 run loadtest/sustained.js
```
BASE_URL permite cambiar el destino. CLIENTS distribuye iteraciones por consumidor;
no equivale al número de VUs. El gateway debe usar --client-header X-Test-Client.
Los ejecutores por tasa mantienen demanda programada independiente de la respuesta.
Cualquier dropped_iterations invalida la carga ofrecida prevista y falla el threshold.
429 es un rechazo esperado: no se suma a http_req_failed. admitted y rejected son
contadores separados, con latencias separadas para evitar mejoras aparentes por rechazo.
Los thresholds de carga verifican transporte y contrato, no imponen porcentajes de
rechazo universales: los cuatro algoritmos tienen semánticas distintas.

Para comparar costos del proxy a baja carga, mide cada configuración separadamente.
No restes percentiles para afirmar latencia por solicitud; reporta distribuciones y
condiciones. /metrics cuenta decisiones, no garantiza que una admisión haya terminado en 2xx.
No existe inferencia automática de abuso ni pruebas distribuidas en v1.
