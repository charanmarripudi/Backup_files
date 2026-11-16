-- The "Golden Record" for Al - combining Master info with current Risk and Violations
WITH vehicles AS (
    SELECT * FROM "hpcl_ceg"."public"."stg_vts_truck_master"
),

risk AS (
    SELECT * FROM "hpcl_ceg"."public"."stg_vts_vehicle_risk"
),

violations_summary AS (
    SELECT 
        vehicle_id,
        SUM(total_violations_count) AS lifetime_violations,
        MAX(event_at) AS last_violation_at
    FROM "hpcl_ceg"."public"."stg_vts_violation_history"
    GROUP BY 1
)

SELECT
    v.vehicle_id,
    v.transporter_name,
    v.region,
    v.zone,
    v.is_blacklisted,
    v.capacity,
    COALESCE(r.current_risk_score, 0) AS current_risk_score,
    COALESCE(vs.lifetime_violations, 0) AS total_violations_count,
    vs.last_violation_at
FROM vehicles v
LEFT JOIN risk r ON v.vehicle_id = r.vehicle_id
LEFT JOIN violations_summary vs ON v.vehicle_id = vs.vehicle_id