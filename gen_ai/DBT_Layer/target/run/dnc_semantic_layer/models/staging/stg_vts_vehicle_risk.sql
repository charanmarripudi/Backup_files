
  create view "hpcl_ceg"."public"."stg_vts_vehicle_risk__dbt_tmp"
    
    
  as (
    -- Snapshot Vehicle Risk
WITH raw AS (
    SELECT * FROM "hpcl_ceg"."public"."tt_risk_score"
)

SELECT
    tt_number AS vehicle_id,
    risk_score AS current_risk_score,
    transporter_name,
    transporter_code,
    total_trips AS lifetime_trips
FROM raw
  );