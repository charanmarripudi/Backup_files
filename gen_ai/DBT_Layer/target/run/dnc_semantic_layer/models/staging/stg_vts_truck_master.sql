
  create view "hpcl_ceg"."public"."stg_vts_truck_master__dbt_tmp"
    
    
  as (
    -- Cleaned and Renamed Truck Master
WITH raw AS (
    SELECT * FROM "hpcl_ceg"."public"."vts_truck_master"
)

SELECT
    truck_no AS vehicle_id,
    transporter_name,
    transporter_code,
    capacity_of_the_truck AS capacity,
    COALESCE(whether_truck_blacklisted, 'N') = 'Y' AS is_blacklisted,
    ownership_type,
    region,
    zone,
    engine_no,
    chassis_no,
    last_changed_date AS last_updated_at
FROM raw
  );