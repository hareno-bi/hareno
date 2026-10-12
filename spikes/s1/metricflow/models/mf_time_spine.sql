select date_key as date_day, fiscal_year,
       'FY' || fiscal_year || 'Q' || fiscal_quarter as fiscal_quarter
from mart.dim_date
