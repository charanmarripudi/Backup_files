import os
import json
import logging
import re
import openai
import urdhva_base
from typing import List, Dict, Optional, Tuple
from sqlalchemy import create_engine, text
from orchestrator.dbconnector import credential_loader

# Configure Logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s: %(message)s')
logger = logging.getLogger("DBT_Orchestrator")

class DBTSemanticOrchestrator:
    """
    HEART OF THE SEMANTIC LAYER
    Handles JSON-based deterministic mapping with OpenAI fallback.
    """
    
    def __init__(self, semantic_map_path: Optional[str] = None):
        if not semantic_map_path:
            # Default to the local map in the same folder
            semantic_map_path = os.path.join(os.path.dirname(__file__), "semantic_map.json")
            
        self.semantic_map = self._load_map(semantic_map_path)
        self.api_key = urdhva_base.settings.openai_api_key
        # Use environment variable as fallback if settings is empty
        if not self.api_key:
            self.api_key = os.getenv("OPENAI_API_KEY")
            
        self.client = openai.OpenAI(api_key=self.api_key)
        
        # Initialize Engine for data retrieval
        self.engine = self.get_db_engine()

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
        
    def _load_map(self, path: str) -> Dict:
        try:
            with open(path, 'r') as f:
                return json.load(f)["semantic_layer"]
        except Exception as e:
            logger.error(f"Failed to load semantic map: {e}")
            return {}

    async def generate_query(self, question: str) -> Dict:
        """
        Main entry point: Intent Detection -> Mapping -> Generation
        """
        # 1. Detect Intent (Metrics & Dimensions)
        metrics, dimensions = self._identify_entities(question)
        
        # 2. Strategy Selection
        if metrics and dimensions:
            logger.info(f"Found match in Semantic Map. Metric: {metrics}, Dim: {dimensions}")
            return await self._generate_deterministic_sql(metrics, dimensions, question)
        else:
            logger.info("Detailed dimensions not found in JSON. Falling back to OpenAI Discovery.")
            return await self._generate_openai_fallback(question)

    def _identify_entities(self, question: str) -> Tuple[List, List]:
        """Scans the question for known metrics and dimensions in the JSON."""
        found_metrics = []
        found_dims = []
        
        # Clean the question into a set of keywords
        q_clean = question.lower().replace('-', ' ').replace('_', ' ')
        q_words = set(re.findall(r'\w+', q_clean))
        
        for table_name, table_info in self.semantic_map["tables"].items():
            for col_name, col_info in table_info["columns"].items():
                # Get column keywords
                clean_col = col_name.lower().replace('_', ' ')
                col_words = set(re.findall(r'\w+', clean_col))
                
                # Logic: Match if there's significant overlap
                is_match = False
                
                # 1. Direct Phrase Match
                if clean_col in q_clean or q_clean in clean_col:
                    is_match = True
                
                # 2. Strong Keyword Overlap
                if not is_match and col_words:
                    intersection = col_words & q_words
                    # If it's a multi-word column, match if at least 2 words overlap
                    # If it's a single-word column, match if that word overlaps
                    if len(col_words) > 1:
                        if len(intersection) >= 2 or (len(intersection) >= 1 and any(w in ["violation", "risk", "trip"] for w in intersection)):
                            is_match = True
                    elif len(intersection) >= 1:
                        is_match = True
                
                if is_match:
                    entity = {"table": table_name, "column": col_name, "info": col_info}
                    if col_info["type"] == "metric":
                        found_metrics.append(entity)
                    else:
                        found_dims.append(entity)
                        
        return found_metrics, found_dims

    async def _generate_deterministic_sql(self, metrics: List, dimensions: List, question: str) -> Dict:
        """Generates SQL directly from DBT Marts using the JSON rules."""
        # Logic to pick the best table (simplified for prototype)
        table = metrics[0]["table"]
        metric_col = metrics[0]["column"]
        dim_col = dimensions[0]["column"]
        agg = metrics[0]["info"].get("aggregation", "SUM")
        
        # Use Marts instead of Raw (e.g., dim_vehicles_metrics)
        mart_table = f"dim_{table}_metrics" if "risk" in table else f"fct_{table}_trends"
        if table == "vts_truck_master": mart_table = "dim_vehicles_metrics"

        sql = f"SELECT {dim_col}, {agg}({metric_col}) FROM {mart_table} GROUP BY 1"
        
        return {
            "success": True,
            "sql": sql,
            "source": "DBT_Semantic_Map",
            "message": "Generated using pre-defined business rules."
        }

    async def _generate_openai_fallback(self, question: str) -> Dict:
        """Uses OpenAI to discover intent when the JSON map isn't enough."""
        
        prompt = f"""
        You are a Data Discovery Expert for a DBT-based Semantic Layer.
        User Question: {question}
        
        Available Models & Schema (VTS Domain):
        1. dim_vehicles:
           - truck_no (PK), transporter_name, region, zone, is_blacklisted, vehicle_type, capacity
        2. fct_violations:
           - truck_no (FK), violation_date (DATE), violation_type, violation_count, location_name
        3. fct_alerts:
           - truck_no (FK), alert_created_at (TIMESTAMP), severity, alert_category, alert_status
        4. fct_ongoing_trips:
           - trip_id, truck_no (FK), driver_name, start_time, current_location
        5. fct_vehicle_risk:
           - truck_no (FK), risk_score, risk_date
        
        Join Rules:
        - Join Facts to Dims on truck_no.
        
        Task:
        1. Identify the metrics and filters.
        2. Generate valid PostgreSQL SQL.
        3. Use table aliases (v, f, s).
        4. ALWAYS use ILIKE for string filters (e.g., region ILIKE 'North%') to be case-insensitive and robust.
        
        Return ONLY the SQL.
        """
        
        response = self.client.chat.completions.create(
            model="gpt-4o",
            messages=[{"role": "system", "content": prompt}]
        )
        
        sql = response.choices[0].message.content.strip()
        
        return {
            "success": True,
            "sql": sql,
            "source": "OpenAI_Fallback_Discovery",
            "message": "Dimensions discovered via OpenAI because they were not in the fixed map."
        }

    async def execute_and_return(self, question: str) -> Dict:
        """
        Generates SQL AND executes it against the database.
        """
        gen_result = await self.generate_query(question)
        if not gen_result["success"]:
            return gen_result

        sql = gen_result["sql"]
        # Remove any Markdown code block formatting if present from OpenAI
        sql = re.sub(r"```sql\n?|```", "", sql).strip()
        
        try:
            with self.engine.connect() as conn:
                result = conn.execute(text(sql))
                columns = result.keys()
                data = [dict(zip(columns, row)) for row in result.fetchall()]
                
                return {
                    "success": True,
                    "sql_query": sql,
                    "data": data,
                    "answer": f"Found {len(data)} results for your question.",
                    "source": gen_result["source"],
                    "validation_log": gen_result["message"]
                }
        except Exception as e:
            logger.error(f"Execution failed: {e}")
            return {
                "success": False,
                "sql_query": sql,
                "data": [],
                "answer": f"Query execution failed: {str(e)}",
                "source": gen_result["source"]
            }
