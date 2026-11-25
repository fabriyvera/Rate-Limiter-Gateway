# ADR 005 — Panel de experimentación local

Estado: aceptado. Fecha: 2026-11-23.

Se amplía v1 con una interfaz local para los cuatro algoritmos existentes, sin
modificar su política ni añadir dependencias Go o JavaScript. HTML, CSS y JS se
embeben en un nuevo comando `lab`, que compone el gateway y un backend demo.
El gateway CLI continúa siendo independiente.

Las pruebas de carga se ejecutan con un proceso k6 real, detectado al arrancar.
Si falta, las solicitudes manuales siguen disponibles y el panel informa cómo
instalarlo. No se imita una prueba k6 con fetch desde el navegador.

Una ejecución usa una configuración inmutable y comienza con cuotas nuevas.
La configuración y las solicitudes manuales quedan bloqueadas mientras corre k6.
La serialización del control y el intercambio atómico del gateway permiten atender
tráfico sin leer configuración parcialmente actualizada. Solicitudes que ya estaban
en vuelo al aplicar una política pueden terminar con la instancia anterior.

El servidor de control escucha exclusivamente en loopback, comprueba Host/Origin,
rechaza claves JSON desconocidas y limita tamaño de entrada, parámetros, historial
y logs. No es un panel de administración público. Exportar JSON es el mecanismo
de conservación de resultados; no se introduce base de datos en esta etapa.
