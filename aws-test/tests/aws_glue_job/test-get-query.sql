select
  arn,
  name,
  description,
  region
from
  aws_glue_job 
where 
  name = '{{ resourceName }}';
