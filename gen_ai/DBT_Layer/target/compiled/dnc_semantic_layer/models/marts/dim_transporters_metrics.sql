-- Transporter Performance Mart
WITH transporter_master AS (
    SELECT DISTINCT transporter_code, transporter_name
    FROM "hpcl_ceg"."public"."stg_vts_truck_master"
),

fleet_risk AS (
    SELECT * FROM "hpcl_ceg"."public"."transporter_risk_score"
),

violation_totals AS (
    SELECT 
        v.transporter_name,
        SUM(total_violations_count) AS fleet_violations,
        COUNT(DISTINCT vehicle_id) AS vehicle_count
    FROM "hpcl_ceg"."public"."stg_vts_violation_history" v
    GROUP BY 1
)

SELECT
    tm.transporter_name,
    tm.transporter_code,
    fr.risk_score AS current_fleet_risk,
    vt.fleet_violations,
    vt.vehicle_count,
    ROUND(CAST(vt.fleet_violations AS NUMERIC) / NULLIF(vt.vehicle_count, 0), 2) AS violations_per_vehicle
FROM transporter_master tm
LEFT JOIN fleet_risk fr ON tm.transporter_code = fr.transporter_code
LEFT JOIN violation_totals vt ON tm.transporter_name = vt.transporter_name