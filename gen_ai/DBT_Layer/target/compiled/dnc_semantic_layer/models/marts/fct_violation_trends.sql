-- AI ready Violation Trends
SELECT
    vehicle_id,
    region,
    zone,
    event_at::DATE AS violation_date,
    total_violations_count,
    stoppage_violations_count,
    route_deviation_count,
    speed_violation_count,
    night_driving_count
FROM "hpcl_ceg"."public"."stg_vts_violation_history"