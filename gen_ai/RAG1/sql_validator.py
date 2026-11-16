

import os
import re 
import logging
import dotenv
from sqlalchemy import create_engine, text, inspect
from typing import Tuple, Optional, Dict, List
try:
    from fuzzywuzzy import process as fuzzy_process
except ImportError:
    fuzzy_process = None
    
from orchestrator.dbconnector import credential_loader

class EnhancedSQLValidator:
    """Enhanced validator with smart error detection and intelligent suggestions"""
    
    _instance = None

    def __new__(cls, *args, **kwargs):
        if cls._instance is None:
            cls._instance = super(EnhancedSQLValidator, cls).__new__(cls)
            cls._instance._initialized = False
        return cls._instance

    def __init__(self, sql_rules):
        if self._initialized:
            return
        self.sql_rules = sql_rules
        self.engine = self.get_db_engine()
        self.real_schema = self._extract_real_schema()
        self.allowed_tables = set(self.real_schema.keys())
        self.allowed_columns = self._extract_allowed_columns_from_db()
        
        # NEW: Build intelligent column suggestions
        self.validation_cache = {}
        self.error_cache = {}  # Added for error analysis caching
        self.common_errors = self._build_error_patterns()
        self.error_frequency = {}
        
        # NEW: Build intelligent column suggestions
        self.column_similarity_index = self._build_column_similarity_index()
        
        # Performance tuning configuration
        self.validation_config = {
            'max_cache_size': 1000,  # Limit cache to prevent memory issues
            'enable_parallel_checks': True,  # Enable parallel validation stages
            'fast_path_enabled': True,  # Enable fast-path security checks
        }
        
        logging.info(f" Enhanced Validator: {len(self.allowed_tables)} tables, {len(self.allowed_columns)} columns")
        logging.info(" Performance: Validation cache enabled, fast-path checks enabled, parallel stages enabled")
        self._initialized = True
        
    def _build_column_similarity_index(self) -> Dict:
        """Build an index for intelligent column name suggestions"""
        similarity_index = {}
        
        # Common misspellings and alternatives
        common_mappings = {
            'vehicle': ['tt_number', 'tl_number', 'truck_no', 'vehicle_number', 'trucknumber'],
            'blacklist': ['whether_truck_blacklisted'],
            'blacklisted': ['whether_truck_blacklisted'], 
            # 'blocked' is intentionally omitted. It refers to a status in the 'alerts' table (vehicle_unblocked_date IS NULL)
            # and is handled by deterministic synonym rules, not column similarity.
            'transporter': ['transporter_name', 'transporter_code'],
            'location': ['location_name', 'vehicle_location'],
            'datetime': ['created_at', 'vts_end_datetime', 'createdat'],
            'invoice': ['invoice_number', 'invoicenumber', 'invoice_no'],
            'risk': ['risk_score'],
            'device removed': ['dr'],
            'power disconnect': ['pd'],
            'route deviation': ['rd'],
            'stoppage violation': ['st'],
            'engine': ['engine_no'],
            'chassis': ['chassis_no'],
        }
        
        for concept, columns in common_mappings.items():
            similarity_index[concept] = columns
        
        return similarity_index
        
    def _build_error_patterns(self) -> Dict:
        """Enhanced error patterns with more coverage"""
        return {
            'missing_table': {
                'pattern': r'missing FROM-clause entry for table "(\w+)"',
                'fix': 'Add table to FROM clause or remove references',
                'severity': 'high'
            },
            'ambiguous_column': {
                'pattern': r'column reference "(\w+)" is ambiguous',
                'fix': 'Prefix with table alias (e.g., table.column)',
                'severity': 'high'
            },
            'undefined_column': {
                'pattern': r'column "(\w+)" does not exist',
                'fix': 'Check column name against schema',
                'severity': 'high'
            },
            'reserved_keyword_alias': {
                'pattern': r'syntax error at or near "(do|case|select|table|from|where)"',
                'fix': 'You used a reserved keyword (like "do") as an alias. Change the alias name (e.g., use "t1" or "d_off").',
                'severity': 'high'
            },
            'invalid_operator': {
                'pattern': r'operator does not exist.*character varying.*\+',
                'fix': 'Use || for string concatenation, not +',
                'severity': 'medium'
            },
            'group_by_error': {
                'pattern': r'must appear in the GROUP BY clause',
                'fix': 'Add all non-aggregated SELECT columns to GROUP BY',
                'severity': 'high'
            },
            'aggregate_in_join': {
                'pattern': r'aggregate functions are not allowed in JOIN conditions',
                'fix': 'Move the aggregate condition to a HAVING clause or use a CTE to pre-calculate.',
                'severity': 'high'
            },
            'timestamp_vs_int': {
                'pattern': r'operator does not exist: timestamp.*integer',
                'fix': 'Do not compare timestamp with integer. Use date < CURRENT_DATE - INTERVAL \'X days\'.',
                'severity': 'high'
            },
            'wrong_table_reference': {
                'pattern': r'relation "(\w+)" does not exist',
                'fix': 'Check table name spelling and ensure table exists',
                'severity': 'high'
            },
            'malformed_array': {
                'pattern': r'malformed array literal',
                'fix': 'You are treating a string as an array or vice versa. Use array_to_string() or ARRAY[] constructor.',
                'severity': 'high'
            }
        }
        
    def _extract_real_schema(self):
        """Extract ACTUAL schema from database"""
        schema = {}
        inspector = inspect(self.engine)
        
        tables = ['vts_alert_history', 'vts_truck_master', 'vts_ongoing_trips', 'alerts',
                  'vts_tripauditmaster', 'tt_risk_score', 'transporter_risk_score', 'completed_trips_risk_score']
        
        for table in tables:
            try:
                columns = inspector.get_columns(table)
                schema[table] = [col['name'] for col in columns]
                logging.info(f"   {table}: {len(columns)} columns")
            except Exception as e:
                logging.error(f"   {table}: {e}")
                
        return schema
    
    def _extract_allowed_columns_from_db(self):
        """Extract all allowed columns from actual database"""
        columns = set()
        for table_cols in self.real_schema.values():
            columns.update(table_cols)
        return columns
    
    def validate_sql(self, sql: str, bypass_forbidden_joins: bool = False, return_cost: bool = False) -> Tuple[bool, str, Optional[float]]:
        """OPTIMIZED: Fast parallel validation <1s - 4-stage pipeline"""
        if not sql or sql.strip() == "":
            return False, "Empty SQL query", None
        
        sql_hash = hash(sql)
        # FASTEST PATH: Cache hit (O(1) dict lookup)
        if sql_hash in self.validation_cache:
            cached_val = self.validation_cache[sql_hash]
            if len(cached_val) == 2:
                return cached_val[0], cached_val[1], None
            return cached_val
        
        sql_lower = sql.lower()
        
        # === STAGE 1: SECURITY & SYNTAX CHECKS (O(n), <50ms) ===
        # Check destructive operations first (fastest reject)
        dangerous_ops = ["delete", "update", "insert", "drop", "alter", "truncate"]
        if any(op in sql_lower for op in dangerous_ops):
            self.validation_cache[sql_hash] = (False, "Destructive SQL blocked")
            return False, "Destructive SQL blocked", None
        
        # === STAGE 2: COLUMN & SYNTAX VALIDATION (<100ms) ===
        if not return_cost:
            col_valid, col_error = self.validate_column_references(sql)
            if not col_valid:
                self._track_error('column_reference_error')
                self.validation_cache[sql_hash] = (False, col_error)
                return False, col_error, None
        
        # === STAGE 2.5: SCHEMA-BASED VALIDATION (NEW - <50ms) ===
        # Validate against SCHEMA_DESCRIPTIONS to catch hallucinations
        schema_error = self._validate_against_schema_descriptions(sql)
        if schema_error:
            self._track_error('schema_validation_error')
            self.validation_cache[sql_hash] = (False, schema_error)
            logging.warning(f"SCHEMA VALIDATION FAILED: {schema_error}")
            return False, schema_error, None
        
        # Pre-validation for syntax issues
        pre_check_result = self._pre_validate_sql(sql)
        if not pre_check_result["valid"]:
            self._track_error('pre_validation_error')
            self.validation_cache[sql_hash] = (False, pre_check_result["error"])
            return False, pre_check_result["error"], None
        
        # === STAGE 3: BUSINESS LOGIC CHECKS (<100ms) ===
        if not bypass_forbidden_joins:
            is_forbidden, forbidden_error = self._check_forbidden_joins(sql)
            if is_forbidden:
                self._track_error('forbidden_join_error')
                self.validation_cache[sql_hash] = (False, forbidden_error)
                return False, forbidden_error, None
        
        # Temporal sanity check
        temporal_valid, temporal_error = self._check_temporal_sanity(sql)
        if not temporal_valid:
            self._track_error('temporal_logic_error')
            self.validation_cache[sql_hash] = (False, temporal_error)
            return False, temporal_error, None
        
        # === STAGE 4: DATABASE EXECUTION TEST (EXPLAIN only, <500ms) ===
        try:
            with self.engine.connect() as conn:
                # Use EXPLAIN (dry-run, no actual execution)
                explain_sql = f"EXPLAIN {sql}"
                result = conn.execute(text(explain_sql)).fetchone()
                
                cost = 0.0
                if return_cost and result:
                    cost_match = re.search(r"cost=\d+\.\d+\.\.(\d+\.\d+)", str(result[0]))
                    if cost_match:
                        cost = float(cost_match.group(1))
                
                # Cache successful validation
                if not return_cost:
                    self.validation_cache[sql_hash] = (True, "Valid")
                return True, "Valid", cost

        except Exception as e:
            error_msg = str(e)
            self._track_error('execution_error')
            detailed_error = self._analyze_error(error_msg, sql)
            self.validation_cache[sql_hash] = (False, detailed_error)
            return False, detailed_error, None
    
    def _check_temporal_sanity(self, sql: str) -> Tuple[bool, str]:
        """Check for logical temporal errors (e.g. start > end, future dates)"""
        sql_lower = sql.lower()
        
        # Check 1: Start date greater than End date in WHERE clause
        # Matches patterns like: date > '2023-01-01' AND date < '2022-01-01'
        # This is a basic heuristic check
        if "between" in sql_lower:
            # Regex to find BETWEEN 'date1' AND 'date2'
            matches = re.findall(r"between\s+'(\d{4}-\d{2}-\d{2})'\s+and\s+'(\d{4}-\d{2}-\d{2})'", sql_lower)
            for start, end in matches:
                if start > end:
                    return False, f" Temporal Logic Error: Start date '{start}' is after end date '{end}'."
            
            # NEW: Check for subqueries inside BETWEEN, which often cause cardinality violations.
            if re.search(r"between\s*\((select)", sql_lower):
                return False, "Logical Error: Using a subquery inside a BETWEEN clause is highly likely to cause a cardinality violation. Use a JOIN or EXISTS clause instead to compare against other rows."

        
        return True, ""

    def _check_forbidden_joins(self, sql: str) -> Tuple[bool, str]:
        """Check for forbidden table combinations based on DATA_MODEL."""
        sql_lower = sql.lower()
        # Find all table names mentioned in the query
        detected_tables = {table for table in self.allowed_tables if re.search(r'\b' + table + r'\b', sql_lower)}

        if len(detected_tables) < 2:
            return False, "" # Not a join, so no forbidden join is possible

        forbidden_rules = self.sql_rules.get("DATA_MODEL", {}).get("forbidden_joins", [])
        for rule in forbidden_rules:
            if rule.issubset(detected_tables):
                # --- FIX: Allow the join ONLY IF it is correctly bridged by the master table ---
                if "vts_truck_master" in detected_tables:
                    continue
                return True, f" Forbidden Join: Joining tables '{' and '.join(rule)}' directly is not allowed. You must join through a master table like vts_truck_master, or aggregate data in a CTE first."

        return False, ""

    def _track_error(self, error_type: str):
        """Track error frequency for learning"""
        self.error_frequency[error_type] = self.error_frequency.get(error_type, 0) + 1
    
    def _analyze_error(self, error: str, sql: str) -> str:
        """OPTIMIZED: Fast error pattern matching with early exit"""
        error_lower = error.lower()
        
        # Try each pattern (fast early exit on first match)
        for error_type, pattern_data in self.common_errors.items():
            if re.search(pattern_data['pattern'], error, re.IGNORECASE):
                match = re.search(pattern_data['pattern'], error, re.IGNORECASE)
                if match:
                    problem = match.group(1) if match.groups() else 'unknown'
                    
                    # Fast context-specific fixes
                    if error_type == 'undefined_column':
                        return self._suggest_column_fix(problem, sql)
                    elif error_type in ('missing_table', 'wrong_table_reference'):
                        return self._suggest_table_fix(problem, error_type == 'wrong_table_reference')
                    elif error_type == 'ambiguous_column':
                        return self._suggest_alias_fix(problem, sql)
                    elif error_type == 'group_by_error':
                        return self._suggest_group_by_fix(sql, error)
                    else:
                        return f"{error_type.upper()}: '{problem}' - {pattern_data['fix']}"
        
        # Fallback: return short error (avoid processing long errors)
        return f"SQL error: {error[:150]}"
    
    def _suggest_group_by_fix(self, sql: str, error: str) -> str:
        """
        Intelligently suggest a GROUP BY fix by parsing the SQL to find the exact missing columns.
        """
        # Regex to find columns in SELECT that are NOT inside an aggregate function
        # This is a simplified pattern but covers many common cases.
        # It looks for words that are not aggregate functions and are not followed by '('.
        try:
            select_clause_match = re.search(r'SELECT\s+(.*?)\s+FROM', sql, re.IGNORECASE | re.DOTALL)
            if not select_clause_match:
                return "Could not parse SELECT clause to suggest a GROUP BY fix."

            select_clause = select_clause_match.group(1)

            # Find all potential column names/aliases in the SELECT clause
            # This pattern finds words that could be columns, ignoring function calls
            potential_cols = re.findall(r'\b([a-zA-Z0-9_]+\.[a-zA-Z0-9_]+|[a-zA-Z0-9_]+)\b(?!\s*\()', select_clause)
            
            # Filter out keywords that are not columns
            keywords_to_ignore = {'as', 'distinct', 'case', 'when', 'then', 'else', 'end'}
            non_agg_cols = {col.split('.')[-1] for col in potential_cols if col.lower() not in keywords_to_ignore}

            group_by_clause_match = re.search(r'GROUP BY\s+(.*?)(?:\s+HAVING|\s+ORDER BY|\s*$)', sql, re.IGNORECASE | re.DOTALL)
            grouped_cols = set()
            if group_by_clause_match:
                grouped_cols_str = group_by_clause_match.group(1)
                grouped_cols = {col.strip().split('.')[-1] for col in grouped_cols_str.split(',')}

            missing_cols = non_agg_cols - grouped_cols
            if missing_cols:
                return f"GROUP BY ERROR: The query is missing columns in the GROUP BY clause.  FIX: Add `{', '.join(sorted(list(missing_cols)))}` to the GROUP BY clause."
        except Exception:
            # Fallback for safety
            return "GROUP BY ERROR: A non-aggregated column in your SELECT list is missing from the GROUP BY clause. Please add it."
        return "GROUP BY ERROR: Every non-aggregated column in SELECT must be in GROUP BY.  FIX: Review your SELECT columns and add missing ones to GROUP BY."
    
    def _detect_hallucinated_columns(self, sql: str) -> Optional[str]:
        """Detect common hallucinated columns that don't exist in schema"""
        hallucination_patterns = {
            # Status columns that don't exist
            'driver_status': ['active_drivers', 'online_status', 'driver_status', 'is_online', 'online', 'driver_online'],
            'vehicle_status': ['vehicle_status', 'is_active', 'vehicle_active', 'status'],
            'gps_accuracy': ['gps_accuracy', 'accuracy_percentage', 'location_accuracy', 'gps_signal', 'signal_strength'],
            'driver_license_status': ['driver_license_status', 'license_status', 'certification', 'driver_certification'],
            'emission_rating': ['emission_rating', 'emission_level', 'emissions', 'co2_emissions'],
            'fuel_consumption': ['fuel_consumption', 'fuel_consumed', 'liters_per_km', 'mileage_per_liter'],
            'maintenance': ['maintenance_logs', 'maintenance_date', 'service_date', 'last_service', 'pending_maintenance'],
            'driver_certifications': ['driver_certifications', 'certifications', 'licenses', 'qualifications'],
            'insurance': ['insurance_claims', 'insurance_table', 'policy_number', 'claim_amount'],
            'road_type': ['road_type', 'road_category', 'highway_flag', 'city_road'],
            'delivery_status': ['delivery_status', 'delivery_completed', 'package_status'],
        }
        
        sql_lower = sql.lower()
        for concept, col_patterns in hallucination_patterns.items():
            for pattern in col_patterns:
                # Check if column is referenced directly in query
                if re.search(rf'\b{pattern}\b', sql_lower):
                    # Verify it actually exists in schema
                    if pattern not in self.allowed_columns:
                        return f"HALLUCINATION ERROR: Your query references '{pattern}' which does not exist in the database. This column was likely invented by the LLM. Please check the actual schema and use correct column names."
        
        return None
    
    def _validate_against_schema_descriptions(self, sql: str) -> Optional[str]:
        """NEW: Validate query against SCHEMA_DESCRIPTIONS from training_data.py
        
        This layer checks:
        1. All columns used actually exist in schema
        2. Categorical values are valid (e.g., 'Open', 'Close' for alert_status)
        3. Boolean flags use correct values (Y/N, true/false)
        4. No hallucinated columns are used
        """
        try:
            from orchestrator.gen_ai.RAG1.training_data import SCHEMA_DESCRIPTIONS, COLUMN_PROFILES
        except ImportError:
            logging.warning("Could not load SCHEMA_DESCRIPTIONS - skipping schema validation")
            return None
        
        # Build map of all valid columns per table
        schema_columns = {}
        for table_name, table_info in SCHEMA_DESCRIPTIONS.items():
            schema_columns[table_name] = set(table_info['columns'].keys())
        
        # Extract all column references from query (table.column or just column)
        sql_lower = sql.lower()
        
        # Pattern 1: table.column references
        table_col_pattern = r'\b([a-z_][a-z0-9_]*)\s*\.\s*([a-z_][a-z0-9_]*)'
        table_col_refs = re.findall(table_col_pattern, sql_lower, re.IGNORECASE)
        
        for table_alias, column in table_col_refs:
            # Map common aliases to table names
            alias_map = {
                'a': 'alerts',
                'vah': 'vts_alert_history',
                'vot': 'vts_ongoing_trips',
                'vtm': 'vts_truck_master',
                'tam': 'vts_tripauditmaster',
                'trs': 'tt_risk_score',
                'ctrs': 'completed_trips_risk_score',
                'md': 'vts_truck_master'
            }
            
            actual_table = alias_map.get(table_alias, table_alias)
            
            if actual_table in schema_columns:
                if column not in schema_columns[actual_table]:
                    # Column doesn't exist in this table
                    valid_cols = sorted(list(schema_columns[actual_table]))
                    suggestion = ""
                    if fuzzy_process:
                        best_match = fuzzy_process.extractOne(column, valid_cols)
                        if best_match and best_match[1] > 70:
                            suggestion = f" Did you mean '{best_match[0]}'?"
                    return f"SCHEMA ERROR: Column '{column}' does not exist in table '{actual_table}'.{suggestion} Valid columns are: {', '.join(valid_cols)}"
        
        # Pattern 2: Check for literal values in WHERE clauses (categorical validation)
        # Look for alert_status = 'value' patterns (CASE-INSENSITIVE)
        if 'alert_status' in sql_lower:
            alert_status_matches = re.findall(r"alert_status\s*=\s*['\"]([^'\"]+)['\"]", sql, re.IGNORECASE)
            valid_alert_status = ['Open', 'Close', 'In Progress']
            valid_alert_status_lower = [v.lower() for v in valid_alert_status]
            for value in alert_status_matches:
                if value.lower() not in valid_alert_status_lower:
                    return f"VALUE ERROR: alert_status value '{value}' is invalid. Valid values: {', '.join(valid_alert_status)}"
        
        # Check for whether_truck_blacklisted values (Y/N are case-insensitive)
        if 'whether_truck_blacklisted' in sql_lower:
            blacklist_matches = re.findall(r"whether_truck_blacklisted\s*=\s*['\"]([^'\"]+)['\"]", sql, re.IGNORECASE)
            valid_blacklist = ['Y', 'N']
            valid_blacklist_lower = [v.lower() for v in valid_blacklist]
            for value in blacklist_matches:
                if value.lower() not in valid_blacklist_lower:
                    return f"VALUE ERROR: whether_truck_blacklisted value '{value}' is invalid. Valid values: Y (Blacklisted) or N (Not Blacklisted)"
        
        # Check for severity values (CASE-INSENSITIVE)
        if 'severity' in sql_lower:
            severity_matches = re.findall(r"severity\s*=\s*['\"]([^'\"]+)['\"]", sql, re.IGNORECASE)
            valid_severity = ['Critical', 'High', 'Medium', 'Low']
            valid_severity_lower = [v.lower() for v in valid_severity]
            for value in severity_matches:
                if value.lower() not in valid_severity_lower:
                    return f"VALUE ERROR: severity value '{value}' is invalid. Valid values: {', '.join(valid_severity)}"
        
        # Check for alert_section (must be 'VTS' - case-insensitive)
        if 'alert_section' in sql_lower:
            alert_section_matches = re.findall(r"alert_section\s*=\s*['\"]([^'\"]+)['\"]", sql, re.IGNORECASE)
            for value in alert_section_matches:
                if value.upper() != 'VTS':
                    return f"VALUE ERROR: alert_section must always be 'VTS'. Found '{value}'"
        
        # Pattern 3: Check for known hallucinated columns
        hallucinated = ['driver_status', 'gps_accuracy', 'online_status', 'driver_online', 
                       'vehicle_status', 'is_active', 'license_status', 'delivery_status', 'road_type']
        for col in hallucinated:
            if col in sql_lower:
                return f"HALLUCINATION: Column '{col}' does not exist in database. This was likely invented by the LLM."
        
        return None
    
    def validate_column_references(self, sql: str) -> Tuple[bool, str]:
        # First check for hallucinated columns
        hallucination_error = self._detect_hallucinated_columns(sql)
        if hallucination_error:
            return False, hallucination_error
        """Enhanced validation for common column reference errors"""
        """Enhanced validation for common column reference errors and hallucinated columns"""
        common_errors = {
            'vah.vehicle_number': 'vah.tl_number',
            'a.device_tamper_count': 'vah.device_tamper_count',
            'a.device_offline_count': 'vah.device_offline_count',
            'vtm.blacklist': 'vtm.whether_truck_blacklisted',
            'blacklisted': 'whether_truck_blacklisted',
            'is_blacklisted': 'whether_truck_blacklisted',
            'violation_types': 'violation_type',
            'capacity': 'capacity_of_the_truck',
            'vtm.city': 'alerts.city',
            'vtm.state': 'alerts.state',
            'vtm.district': 'alerts.district',
            'vtm.sales_area': 'alerts.sales_area',
        }
        
        for wrong, correct in common_errors.items():
            if re.search(r'\b' + re.escape(wrong) + r'\b', sql, re.IGNORECASE):
                return False, f"Invalid column reference: '{wrong}' -> use '{correct}'"

        sql_lower = sql.lower()
        col_pattern = r'\b([a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*|[a-z_][a-z0-9_]+)(?!\s*\()'
        potential_cols = re.findall(col_pattern, sql_lower)
        
        return True, "Column references OK"
    
        mentioned_columns = set()
        for col in potential_cols:
            mentioned_columns.add(col.split('.')[-1])
        
        keywords = {'select', 'from', 'where', 'join', 'on', 'group', 'by', 'having', 'order', 'limit', 'distinct', 'as', 'and', 'or', 'not', 'in', 'exists', 'between', 'like', 'case', 'when', 'then', 'else', 'end', 'union', 'all', 'desc', 'asc', 'is', 'null', 'lateral', 'unnest'}
        known_aliases = {'vah', 'vtm', 'vot', 'a', 'trs', 'tam', 'md'}
        mentioned_columns = mentioned_columns - keywords - self.allowed_tables - known_aliases
        
        invalid_cols = mentioned_columns - self.allowed_columns
        if invalid_cols:
            first_invalid = sorted(list(invalid_cols))[0]
            suggestion = self._suggest_column_fix(first_invalid, sql)
            return False, suggestion
        
        return True, "Column references seem valid."
    
    def _pre_validate_sql(self, sql: str) -> Dict:
        """Enhanced pre-validation with more checks"""
        
        # Check for trailing commas
        if re.search(r',\s*\n\s*FROM', sql, re.IGNORECASE):
            return {
                "valid": False,
                "error": " Trailing comma before FROM clause. Remove the comma after the last SELECT column."
            }
        
        # Check for string concatenation errors
        if re.search(r"'[^']*'\s*\+\s*", sql) or re.search(r"\s*\+\s*'[^']*'", sql):
            return {
                "valid": False, 
                "error": " Invalid string concatenation. Use || operator instead of +"
            }
        
        return {"valid": True, "error": None}

    def _suggest_column_fix(self, column: str, sql: str = "") -> str:
        """OPTIMIZED: Fast column suggestions with caching"""
        column_lower = column.lower()
        
        # CACHE CHECK first (fastest path)
        cache_key = f"col_fix_{column_lower}"
        if cache_key in self.validation_cache:
            return self.validation_cache[cache_key]
        
        similar = []
        
        # 1. Check similarity index (fastest - pre-computed mappings)
        for concept, columns in self.column_similarity_index.items():
            if concept in column_lower or column_lower in concept:
                similar.extend(columns)
        
        # 2. Check schema columns (only if not found in index)
        if not similar:
            for col in self.allowed_columns:
                if column_lower in col.lower() or col.lower() in column_lower:
                    similar.append(col)
        
        # Remove duplicates, limit to top 2 (fast)
        similar = list(dict.fromkeys(similar))[:2]
        
        if similar:
            result = f"Column '{column}' not found. Did you mean: {', '.join(similar)}?"
        else:
            # Cross-table lookup (only if no similarity match)
            tables_containing_col = []
            for table, cols in self.real_schema.items():
                if column_lower in [c.lower() for c in cols]:
                    tables_containing_col.append(table)
            
            if tables_containing_col and 'vts_truck_master' in tables_containing_col:
                result = f"Column '{column}' found in vts_truck_master. JOIN it to use this column."
            elif tables_containing_col:
                result = f"Column '{column}' found in: {tables_containing_col[0]}. JOIN it to use."
            else:
                result = f"Column '{column}' does not exist in schema."
        
        # Cache result (fast lookup next time)
        self.validation_cache[cache_key] = result
        return result
    
          
    
    def _suggest_table_fix(self, table_or_alias: str, is_wrong_table: bool = False) -> str:
        """OPTIMIZED: Fast table/alias suggestions with caching"""
        # CACHE CHECK (fastest path)
        cache_key = f"tbl_{table_or_alias.lower()}"
        if cache_key in self.validation_cache:
            return self.validation_cache[cache_key]
        
        result = ""
        
        # Pre-defined common aliases (O(1))
        common_aliases = {
            'vah': 'vts_alert_history',
            'vtm': 'vts_truck_master',
            'vot': 'vts_ongoing_trips',
            'a': 'alerts',
            'trs': 'tt_risk_score',
            'tam': 'vts_tripauditmaster'
        }
        
        if table_or_alias in common_aliases:
            table = common_aliases[table_or_alias]
            result = f"Use: FROM {table} {table_or_alias} or JOIN {table} {table_or_alias} ON [condition]"
        elif is_wrong_table:
            # Fast similarity check: substring match only
            matches = [t for t in self.allowed_tables if table_or_alias.lower() in t.lower()]
            if matches:
                result = f"Table '{table_or_alias}' not found. Did you mean: {matches[0]}?"
            else:
                result = f"Table '{table_or_alias}' does not exist in schema."
        else:
            result = f"Alias '{table_or_alias}' not defined. Add to FROM or JOIN clause."
        
        self.validation_cache[cache_key] = result
        return result
    
    
    def _suggest_alias_fix(self, column: str, sql: str) -> str:
        """OPTIMIZED: Fast alias suggestions with caching"""
        # CACHE CHECK (fastest)
        cache_key = f"alias_{column.lower()}"
        if cache_key in self.validation_cache:
            return self.validation_cache[cache_key]
        
        # Fast pre-defined mapping
        table_alias_map = {
            'vts_alert_history': 'vah',
            'vts_truck_master': 'vtm',
            'vts_ongoing_trips': 'vot',
            'alerts': 'a',
            'tt_risk_score': 'trs'
        }
        
        # Find tables with this column (limited search - early exit)
        suggestions = []
        for table, columns in self.real_schema.items():
            if column in columns:
                alias = table_alias_map.get(table, table[:3])
                suggestions.append(f"{alias}.{column}")
                if len(suggestions) >= 2:  # Early exit after 2 matches
                    break
        
        if suggestions:
            result = f"Column '{column}' is ambiguous. Use: {', '.join(suggestions)}"
        else:
            result = f"Column '{column}' is ambiguous. Prefix with table alias: table.{column}"
        
        self.validation_cache[cache_key] = result
        return result

    def get_db_engine(self):
        """Returns the SQLAlchemy engine for the configured database"""
        try:
            creds = credential_loader.get_credentials('APP_DB')
            
            connection_string = (
                f"postgresql+psycopg2://{creds['user']}:{creds['password']}@"
                f"{creds['host']}:{creds['port']}/{creds['database']}"
            )
            engine = create_engine(
                connection_string,
                pool_size=5,
                max_overflow=10,
                pool_pre_ping=True,
                pool_recycle=3600,
            )
            
            with engine.connect() as conn:
                conn.execute(text("SELECT 1"))
            logging.info(" Database connection successful")
            return engine
        except Exception as e:
            logging.error(f" Database connection failed: {e}")
            raise
    
    
    def get_error_stats(self) -> Dict:
        """Return error frequency statistics for analysis"""
        return self.error_frequency.copy()
    
    def get_most_common_errors(self, top_n: int = 5) -> List[Tuple[str, int]]:
        """Get the most common errors for learning"""
        sorted_errors = sorted(self.error_frequency.items(), key=lambda x: x[1], reverse=True)
        return sorted_errors[:top_n]
